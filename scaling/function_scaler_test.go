// License: OpenFaaS Community Edition (CE) EULA
// Copyright (c) 2017,2019-2024 OpenFaaS Author(s)

package scaling

import (
	"errors"
	"testing"
	"time"
)

type fakeServiceQuery struct {
	responses []ServiceQueryResponse
	err       error
	getCalls  int
	setCalls  []uint64
}

func (f *fakeServiceQuery) GetReplicas(service, namespace string) (ServiceQueryResponse, error) {
	f.getCalls++
	if f.err != nil {
		return ServiceQueryResponse{}, f.err
	}
	if len(f.responses) == 0 {
		return ServiceQueryResponse{}, nil
	}

	res := f.responses[0]
	if len(f.responses) > 1 {
		f.responses = f.responses[1:]
	}
	return res, nil
}

func (f *fakeServiceQuery) SetReplicas(service, namespace string, count uint64) error {
	f.setCalls = append(f.setCalls, count)
	return nil
}

func newTestScaler(serviceQuery ServiceQuery) FunctionScaler {
	return NewFunctionScaler(ScalingConfig{
		MaxPollCount:         1,
		SetScaleRetries:      1,
		FunctionPollInterval: 0 * time.Millisecond,
		ServiceQuery:         serviceQuery,
	})
}

func TestScaleHotFunctionQueriesLiveProvider(t *testing.T) {
	serviceQuery := &fakeServiceQuery{
		responses: []ServiceQueryResponse{{AvailableReplicas: 1}},
	}
	scaler := newTestScaler(serviceQuery)

	res := scaler.Scale("echo", "openfaas-fn")

	if !res.Available || !res.Found || res.Error != nil {
		t.Fatalf("expected available result, got %#v", res)
	}
	if serviceQuery.getCalls != 1 {
		t.Fatalf("expected one live GetReplicas call, got %d", serviceQuery.getCalls)
	}
	if len(serviceQuery.setCalls) != 0 {
		t.Fatalf("expected no SetReplicas calls, got %v", serviceQuery.setCalls)
	}
}

func TestScaleSequentialCallsDoNotReuseCachedAvailability(t *testing.T) {
	serviceQuery := &fakeServiceQuery{
		responses: []ServiceQueryResponse{
			{AvailableReplicas: 1},
			{AvailableReplicas: 1},
		},
	}
	scaler := newTestScaler(serviceQuery)

	first := scaler.Scale("echo", "openfaas-fn")
	second := scaler.Scale("echo", "openfaas-fn")

	if !first.Available || !second.Available {
		t.Fatalf("expected both calls available, got first=%#v second=%#v", first, second)
	}
	if serviceQuery.getCalls != 2 {
		t.Fatalf("expected two live GetReplicas calls, got %d", serviceQuery.getCalls)
	}
}

func TestScaleColdFunctionSetsReplicasAndPollsUntilAvailable(t *testing.T) {
	serviceQuery := &fakeServiceQuery{
		responses: []ServiceQueryResponse{
			{AvailableReplicas: 0, Replicas: 0, MinReplicas: 2},
			{AvailableReplicas: 0, Replicas: 0, MinReplicas: 2},
			{AvailableReplicas: 1, Replicas: 2, MinReplicas: 2},
		},
	}
	scaler := newTestScaler(serviceQuery)

	res := scaler.Scale("echo", "openfaas-fn")

	if !res.Available || !res.Found || res.Error != nil {
		t.Fatalf("expected cold function to become available, got %#v", res)
	}
	if serviceQuery.getCalls != 3 {
		t.Fatalf("expected three live GetReplicas calls, got %d", serviceQuery.getCalls)
	}
	if len(serviceQuery.setCalls) != 1 || serviceQuery.setCalls[0] != 2 {
		t.Fatalf("expected SetReplicas to target min replicas, got %v", serviceQuery.setCalls)
	}
}

func TestScaleMissingFunctionReturnsNotFound(t *testing.T) {
	serviceQuery := &fakeServiceQuery{err: errors.New("not found")}
	scaler := newTestScaler(serviceQuery)

	res := scaler.Scale("missing", "openfaas-fn")

	if res.Found || res.Available || res.Error == nil {
		t.Fatalf("expected missing function result, got %#v", res)
	}
	if serviceQuery.getCalls != 1 {
		t.Fatalf("expected one live GetReplicas call, got %d", serviceQuery.getCalls)
	}
}
