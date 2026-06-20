// License: OpenFaaS Community Edition (CE) EULA
// Copyright (c) 2017,2019-2024 OpenFaaS Author(s)

// Copyright (c) Alex Ellis 2017. All rights reserved.

package main

import (
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/openfaas/faas-provider/auth"
	"github.com/openfaas/faas/gateway/handlers"
	"github.com/openfaas/faas/gateway/metrics"
	"github.com/openfaas/faas/gateway/pkg/middleware"
	"github.com/openfaas/faas/gateway/plugin"
	"github.com/openfaas/faas/gateway/scaling"
	"github.com/openfaas/faas/gateway/types"
	"github.com/openfaas/faas/gateway/version"
	natsHandler "github.com/openfaas/nats-queue-worker/handler"
)

// NameExpression for a function / service
const NameExpression = "-a-zA-Z_0-9."

func main() {

	osEnv := types.OsEnv{}
	readConfig := types.ReadConfig{}
	config, configErr := readConfig.Read(osEnv)

	if configErr != nil {
		log.Fatalln(configErr)
	}
	if !config.UseExternalProvider() {
		log.Fatalln("You must provide an external provider via 'functions_provider_url' env-var.")
	}

	fmt.Printf("OpenFaaS Gateway - Community Edition (CE)\n"+
		"\nVersion: %s Commit: %s\nTimeouts: read=%s\twrite=%s\tupstream=%s\tarchive_upload=%s\nFunction provider: %s\n\n",
		version.BuildVersion(),
		version.GitCommitSHA,
		config.ReadTimeout,
		config.WriteTimeout,
		config.UpstreamTimeout,
		config.ArchiveUploadTimeout,
		config.FunctionsProviderURL)

	// credentials is used for service-to-service auth
	var credentials *auth.BasicAuthCredentials

	if config.UseBasicAuth {
		var err error
		reader := auth.ReadBasicAuthFromDisk{
			SecretMountPath: config.SecretMountPath,
		}
		credentials, err = reader.Read()

		if err != nil {
			log.Panic(err.Error())
		}
	}

	var faasHandlers types.HandlerSet

	servicePollInterval := time.Second * 5

	metricsOptions := metrics.BuildMetricsOptions()
	exporter := metrics.NewExporter(metricsOptions, credentials, config.Namespace)
	exporter.StartServiceWatcher(*config.FunctionsProviderURL, metricsOptions, "func", servicePollInterval)
	metrics.RegisterExporter(exporter)

	reverseProxy := types.NewHTTPClientReverseProxy(config.FunctionsProviderURL,
		config.UpstreamTimeout,
		config.MaxIdleConns,
		config.MaxIdleConnsPerHost)

	loggingNotifier := handlers.LoggingNotifier{}

	prometheusNotifier := handlers.PrometheusFunctionNotifier{
		Metrics:           &metricsOptions,
		FunctionNamespace: config.Namespace,
	}

	functionNotifiers := []handlers.HTTPNotifier{loggingNotifier, prometheusNotifier}
	forwardingNotifiers := []handlers.HTTPNotifier{loggingNotifier}
	quietNotifier := []handlers.HTTPNotifier{}

	urlResolver := middleware.SingleHostBaseURLResolver{BaseURL: config.FunctionsProviderURL.String()}
	var functionURLResolver middleware.BaseURLResolver
	var functionURLTransformer middleware.URLPathTransformer
	nilURLTransformer := middleware.TransparentURLPathTransformer{}
	trimURLTransformer := middleware.FunctionPrefixTrimmingURLPathTransformer{}

	functionURLResolver = urlResolver
	functionURLTransformer = nilURLTransformer

	var serviceAuthInjector middleware.AuthInjector

	if config.UseBasicAuth {
		serviceAuthInjector = &middleware.BasicAuthInjector{Credentials: credentials}
	}

	// externalServiceQuery is used to query metadata from the provider about a function
	externalServiceQuery := plugin.NewExternalServiceQuery(*config.FunctionsProviderURL, serviceAuthInjector)

	scalingConfig := scaling.ScalingConfig{
		MaxPollCount:         uint(1000),
		SetScaleRetries:      uint(20),
		FunctionPollInterval: time.Millisecond * 100,
		ServiceQuery:         externalServiceQuery,
	}

	functionQuery := scaling.NewDirectFunctionQuery(externalServiceQuery)

	faasHandlers.Proxy = handlers.MakeCallIDMiddleware(
		handlers.MakeForwardingProxyHandler(reverseProxy, functionNotifiers, functionURLResolver, functionURLTransformer, nil),
	)

	faasHandlers.ListFunctions = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)
	faasHandlers.DeployFunction = handlers.MakeArchiveUploadForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector, config.ArchiveUploadTimeout)
	faasHandlers.DeleteFunction = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)
	faasHandlers.UpdateFunction = handlers.MakeArchiveUploadForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector, config.ArchiveUploadTimeout)
	faasHandlers.FunctionStatus = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)
	faasHandlers.FunctionStats = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)

	faasHandlers.InfoHandler = handlers.MakeInfoHandler(handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector))
	faasHandlers.TelemetryHandler = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, nil)

	faasHandlers.SecretHandler = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)

	faasHandlers.NamespaceListerHandler = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)
	faasHandlers.NamespaceMutatorHandler = handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)
	callgraphHandler := handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector)

	faasHandlers.Alert = handlers.MakeNotifierWrapper(
		handlers.MakeAlertHandler(externalServiceQuery, config.Namespace),
		quietNotifier,
	)

	faasHandlers.LogProxyHandler = handlers.NewLogHandlerFunc(*config.LogsProviderURL, config.WriteTimeout)

	functionProxy := faasHandlers.Proxy

	if config.ScaleFromZero {
		scaler := scaling.NewFunctionScaler(scalingConfig)
		functionProxy = handlers.MakeScalingHandler(functionProxy, scaler, scalingConfig, config.Namespace)
	}

	// Stamp the gateway arrival time before scale-from-zero so the provider can
	// record a clean caller->call-issue gap (independent of the callee's cold
	// start). Async invocations reach the provider via the queue worker's sync
	// re-invoke through this same path, so this covers them too.
	functionProxy = handlers.MakeArrivalStampMiddleware(functionProxy)

	if config.UseNATS() {
		log.Println("Async enabled: Using NATS Streaming")
		log.Println("Deprecation Notice: NATS Streaming is no longer maintained and won't receive updates from June 2023")

		maxReconnect := 60
		interval := time.Second * 2

		defaultNATSConfig := natsHandler.NewDefaultNATSConfig(maxReconnect, interval)

		natsQueue, queueErr := natsHandler.CreateNATSQueue(*config.NATSAddress, *config.NATSPort, *config.NATSClusterName, *config.NATSChannel, defaultNATSConfig)
		if queueErr != nil {
			log.Fatalln(queueErr)
		}

		faasHandlers.QueuedProxy = handlers.MakeNotifierWrapper(
			handlers.MakeCallIDMiddleware(handlers.MakeQueuedProxy(metricsOptions, natsQueue, trimURLTransformer, config.Namespace, functionQuery)),
			forwardingNotifiers,
		)
	}

	prometheusQuery := metrics.NewPrometheusQuery(config.PrometheusHost, config.PrometheusPort, http.DefaultClient, version.BuildVersion())
	faasHandlers.ListFunctions = metrics.AddMetricsHandler(faasHandlers.ListFunctions, prometheusQuery)
	faasHandlers.ScaleFunction = scaling.MakeHorizontalScalingHandler(handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector))

	if credentials != nil {
		faasHandlers.Alert =
			auth.DecorateWithBasicAuth(faasHandlers.Alert, credentials)
		faasHandlers.UpdateFunction =
			auth.DecorateWithBasicAuth(faasHandlers.UpdateFunction, credentials)
		faasHandlers.DeleteFunction =
			auth.DecorateWithBasicAuth(faasHandlers.DeleteFunction, credentials)
		faasHandlers.DeployFunction =
			auth.DecorateWithBasicAuth(faasHandlers.DeployFunction, credentials)
		faasHandlers.ListFunctions =
			auth.DecorateWithBasicAuth(faasHandlers.ListFunctions, credentials)
		faasHandlers.ScaleFunction =
			auth.DecorateWithBasicAuth(faasHandlers.ScaleFunction, credentials)
		faasHandlers.FunctionStatus =
			auth.DecorateWithBasicAuth(faasHandlers.FunctionStatus, credentials)
		faasHandlers.FunctionStats =
			auth.DecorateWithBasicAuth(faasHandlers.FunctionStats, credentials)
		faasHandlers.InfoHandler =
			auth.DecorateWithBasicAuth(faasHandlers.InfoHandler, credentials)
		faasHandlers.SecretHandler =
			auth.DecorateWithBasicAuth(faasHandlers.SecretHandler, credentials)
		faasHandlers.LogProxyHandler =
			auth.DecorateWithBasicAuth(faasHandlers.LogProxyHandler, credentials)
		faasHandlers.NamespaceListerHandler =
			auth.DecorateWithBasicAuth(faasHandlers.NamespaceListerHandler, credentials)
		faasHandlers.NamespaceMutatorHandler =
			auth.DecorateWithBasicAuth(faasHandlers.NamespaceMutatorHandler, credentials)
		callgraphHandler =
			auth.DecorateWithBasicAuth(callgraphHandler, credentials)
	}

	r := newRouter(faasHandlers, functionProxy, callgraphHandler,
		handlers.MakeForwardingProxyHandler(reverseProxy, forwardingNotifiers, urlResolver, nilURLTransformer, serviceAuthInjector))

	//Start metrics server in a goroutine
	go runMetricsServer()

	tcpPort := 8080

	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", tcpPort),
		ReadTimeout:    config.ReadTimeout,
		WriteTimeout:   config.WriteTimeout,
		MaxHeaderBytes: http.DefaultMaxHeaderBytes, // 1MB - can be overridden by setting Server.MaxHeaderBytes.
		Handler:        r,
	}

	log.Fatal(s.ListenAndServe())
}

func newRouter(faasHandlers types.HandlerSet, functionProxy http.HandlerFunc, callgraphHandler http.HandlerFunc, healthzHandler http.HandlerFunc) *mux.Router {
	r := mux.NewRouter()
	// max wait time to start a function = maxPollCount * functionPollInterval

	r.HandleFunc("/function/{name:["+NameExpression+"]+}", functionProxy)
	r.HandleFunc("/function/{name:["+NameExpression+"]+}/", functionProxy)
	r.HandleFunc("/function/{name:["+NameExpression+"]+}/{params:.*}", functionProxy)

	r.HandleFunc("/system/info", faasHandlers.InfoHandler).Methods(http.MethodGet)
	r.HandleFunc("/system/telemetry", faasHandlers.TelemetryHandler).Methods(http.MethodGet)

	r.HandleFunc("/system/alert", faasHandlers.Alert).Methods(http.MethodPost)

	r.HandleFunc("/system/function/{name:["+NameExpression+"]+}", faasHandlers.FunctionStatus).Methods(http.MethodGet)
	r.HandleFunc("/system/stats/function/{name:["+NameExpression+"]+}", faasHandlers.FunctionStats).Methods(http.MethodGet)
	r.HandleFunc("/system/functions", faasHandlers.ListFunctions).Methods(http.MethodGet)
	r.HandleFunc("/system/functions", faasHandlers.DeployFunction).Methods(http.MethodPost)
	r.HandleFunc("/system/functions", faasHandlers.DeleteFunction).Methods(http.MethodDelete)
	r.HandleFunc("/system/functions", faasHandlers.UpdateFunction).Methods(http.MethodPut)
	r.HandleFunc("/system/scale-function/{name:["+NameExpression+"]+}", faasHandlers.ScaleFunction).Methods(http.MethodPost)

	r.HandleFunc("/system/secrets", faasHandlers.SecretHandler).Methods(http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete)
	r.HandleFunc("/system/logs", faasHandlers.LogProxyHandler).Methods(http.MethodGet)

	r.HandleFunc("/system/namespaces", faasHandlers.NamespaceListerHandler).Methods(http.MethodGet)
	r.HandleFunc("/system/namespace/{namespace:["+NameExpression+"]*}", faasHandlers.NamespaceMutatorHandler).
		Methods(http.MethodPost, http.MethodDelete, http.MethodPut, http.MethodGet)
	r.HandleFunc("/system/callgraph", callgraphHandler).Methods(http.MethodGet)
	r.HandleFunc("/system/callgraph/function/{name:["+NameExpression+"]+}", callgraphHandler).Methods(http.MethodGet)
	r.HandleFunc("/system/callgraph/edge", callgraphHandler).Methods(http.MethodGet)

	if faasHandlers.QueuedProxy != nil {
		r.HandleFunc("/async-function/{name:["+NameExpression+"]+}/", faasHandlers.QueuedProxy).Methods(http.MethodPost)
		r.HandleFunc("/async-function/{name:["+NameExpression+"]+}", faasHandlers.QueuedProxy).Methods(http.MethodPost)
		r.HandleFunc("/async-function/{name:["+NameExpression+"]+}/{params:.*}", faasHandlers.QueuedProxy).Methods(http.MethodPost)
	}

	r.HandleFunc("/healthz", healthzHandler).Methods(http.MethodGet)
	r.HandleFunc("/", handlers.HealthzHandler).Methods(http.MethodGet)

	return r
}

// runMetricsServer Listen on a separate HTTP port for Prometheus metrics to keep this accessible from
// the internal network only.
func runMetricsServer() {
	metricsHandler := metrics.PrometheusHandler()
	router := mux.NewRouter()
	router.Handle("/metrics", metricsHandler)
	router.HandleFunc("/healthz", handlers.HealthzHandler)

	port := 8082
	readTimeout := 5 * time.Second
	writeTimeout := 5 * time.Second

	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", port),
		ReadTimeout:    readTimeout,
		WriteTimeout:   writeTimeout,
		MaxHeaderBytes: http.DefaultMaxHeaderBytes,
		Handler:        router,
	}

	log.Fatal(s.ListenAndServe())
}
