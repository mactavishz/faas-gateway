package scaling

import (
	"fmt"
	"log"
	"time"

	"github.com/openfaas/faas/gateway/types"
	"golang.org/x/sync/singleflight"
)

// NewFunctionScaler create a new scaler with the specified
// ScalingConfig
func NewFunctionScaler(config ScalingConfig) FunctionScaler {
	return FunctionScaler{
		Config:       config,
		SingleFlight: &singleflight.Group{},
	}
}

// FunctionScaler scales from zero
type FunctionScaler struct {
	Config       ScalingConfig
	SingleFlight *singleflight.Group
}

// FunctionScaleResult holds the result of scaling from zero
type FunctionScaleResult struct {
	Available bool
	Error     error
	Found     bool
	Duration  time.Duration
}

// Scale scales a function from zero replicas to 1 or the value set in
// the minimum replicas metadata
func (f *FunctionScaler) Scale(functionName, namespace string) FunctionScaleResult {
	start := time.Now()

	getKey := fmt.Sprintf("GetReplicas-%s.%s", functionName, namespace)
	res, err, _ := f.SingleFlight.Do(getKey, func() (interface{}, error) {
		return f.Config.ServiceQuery.GetReplicas(functionName, namespace)
	})

	if err != nil {
		return FunctionScaleResult{
			Error:     err,
			Available: false,
			Found:     false,
			Duration:  time.Since(start),
		}
	}
	if res == nil {
		return FunctionScaleResult{
			Error:     fmt.Errorf("empty response from server"),
			Available: false,
			Found:     false,
			Duration:  time.Since(start),
		}
	}

	// Check if there are available replicas in the live data
	if res.(ServiceQueryResponse).AvailableReplicas > 0 {
		return FunctionScaleResult{
			Error:     nil,
			Available: true,
			Found:     true,
			Duration:  time.Since(start),
		}
	}

	queryResponse := res.(ServiceQueryResponse)

	// Under unified desired/available semantics, scale-up should happen whenever
	// available replicas are 0. Desired replicas are used as the target when set.
	if queryResponse.AvailableReplicas == 0 {
		targetReplicas := queryResponse.Replicas
		if targetReplicas == 0 {
			targetReplicas = uint64(1)
			if queryResponse.MinReplicas > 0 {
				targetReplicas = queryResponse.MinReplicas
			}
		}

		// In a retry-loop, first query desired replicas, then
		// set them if the value is still at 0.
		scaleResult := types.Retry(func(attempt int) error {

			res, err, _ := f.SingleFlight.Do(getKey, func() (interface{}, error) {
				return f.Config.ServiceQuery.GetReplicas(functionName, namespace)
			})

			if err != nil {
				return err
			}

			queryResponse = res.(ServiceQueryResponse)

			// If the function became available while polling, no scale request
			// is needed. Readiness is still confirmed in the polling loop below.
			if queryResponse.AvailableReplicas > 0 {
				return nil
			}

			// Request a scale up to the minimum amount of replicas
			setKey := fmt.Sprintf("SetReplicas-%s.%s", functionName, namespace)

			if _, err, _ := f.SingleFlight.Do(setKey, func() (interface{}, error) {

				log.Printf("[Scale %d/%d] function=%s 0 available => %d requested",
					attempt, int(f.Config.SetScaleRetries), functionName, targetReplicas)

				if err := f.Config.ServiceQuery.SetReplicas(functionName, namespace, targetReplicas); err != nil {
					return nil, fmt.Errorf("unable to scale function [%s], err: %s", functionName, err)
				}
				return nil, nil
			}); err != nil {
				return err
			}

			return nil

		}, "Scale", int(f.Config.SetScaleRetries), f.Config.FunctionPollInterval)

		if scaleResult != nil {
			return FunctionScaleResult{
				Error:     scaleResult,
				Available: false,
				Found:     true,
				Duration:  time.Since(start),
			}
		}

	}

	// Holding pattern for at least one function replica to be available
	for i := 0; i < int(f.Config.MaxPollCount); i++ {

		res, err, _ := f.SingleFlight.Do(getKey, func() (interface{}, error) {
			return f.Config.ServiceQuery.GetReplicas(functionName, namespace)
		})

		totalTime := time.Since(start)

		if err != nil {
			return FunctionScaleResult{
				Error:     err,
				Available: false,
				Found:     true,
				Duration:  totalTime,
			}
		}
		queryResponse := res.(ServiceQueryResponse)

		if queryResponse.AvailableReplicas > 0 {

			log.Printf("[Ready] function=%s waited for - %.4fs", functionName, totalTime.Seconds())

			return FunctionScaleResult{
				Error:     nil,
				Available: true,
				Found:     true,
				Duration:  totalTime,
			}
		}

		time.Sleep(f.Config.FunctionPollInterval)
	}

	return FunctionScaleResult{
		Error:     nil,
		Available: false,
		Found:     true,
		Duration:  time.Since(start),
	}
}
