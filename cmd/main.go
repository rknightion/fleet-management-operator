/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	corev1 "k8s.io/api/core/v1"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/internal/controller"
	"github.com/grafana/fleet-management-operator/pkg/fleetclient"
	"github.com/grafana/fleet-management-operator/pkg/sources"
	httpsource "github.com/grafana/fleet-management-operator/pkg/sources/http"
	sqlsource "github.com/grafana/fleet-management-operator/pkg/sources/sql"
	// +kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(fleetmanagementv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

// nolint:gocyclo
func main() {
	var metricsAddr string
	var metricsCertPath, metricsCertName, metricsCertKey string
	var webhookCertPath, webhookCertName, webhookCertKey string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	var tlsOpts []func(*tls.Config)
	// Per-controller feature flags. Defaults: pipeline on (existing behavior),
	// every other controller off so the chart is backward-compatible.
	var enablePipelineController bool
	var enableCollectorController bool
	var enablePolicyController bool
	var enableExternalSyncController bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	flag.StringVar(&metricsCertPath, "metrics-cert-path", "",
		"The directory that contains the metrics server certificate.")
	flag.StringVar(&metricsCertName, "metrics-cert-name", "tls.crt", "The name of the metrics server certificate file.")
	flag.StringVar(&metricsCertKey, "metrics-cert-key", "tls.key", "The name of the metrics server key file.")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	flag.BoolVar(&enablePipelineController, "enable-pipeline-controller", true,
		"Enable the Pipeline reconciler and webhook.")
	flag.BoolVar(&enableCollectorController, "enable-collector-controller", false,
		"Enable the Collector reconciler and webhook (manages collector remote attributes).")
	flag.BoolVar(&enablePolicyController, "enable-policy-controller", false,
		"Enable the RemoteAttributePolicy reconciler and webhook (bulk attribute assignment by selector).")
	flag.BoolVar(&enableExternalSyncController, "enable-external-sync-controller", false,
		"Enable the ExternalAttributeSync reconciler and webhook (HTTP/SQL-backed scheduled attribute pulls).")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// if the enable-http2 flag is false (the default), http/2 should be disabled
	// due to its vulnerabilities. More specifically, disabling http/2 will
	// prevent from being vulnerable to the HTTP/2 Stream Cancellation and
	// Rapid Reset CVEs. For more information see:
	// - https://github.com/advisories/GHSA-qppj-fm5r-hxr3
	// - https://github.com/advisories/GHSA-4374-p667-p6c8
	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	// Initial webhook TLS options
	webhookTLSOpts := tlsOpts
	webhookServerOptions := webhook.Options{
		TLSOpts: webhookTLSOpts,
	}

	if len(webhookCertPath) > 0 {
		setupLog.Info("Initializing webhook certificate watcher using provided certificates",
			"webhook-cert-path", webhookCertPath, "webhook-cert-name", webhookCertName, "webhook-cert-key", webhookCertKey)

		webhookServerOptions.CertDir = webhookCertPath
		webhookServerOptions.CertName = webhookCertName
		webhookServerOptions.KeyName = webhookCertKey
	}

	webhookServer := webhook.NewServer(webhookServerOptions)

	// Metrics endpoint is enabled in 'config/default/kustomization.yaml'. The Metrics options configure the server.
	// More info:
	// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.0/pkg/metrics/server
	// - https://book.kubebuilder.io/reference/metrics.html
	metricsServerOptions := metricsserver.Options{
		BindAddress:   metricsAddr,
		SecureServing: secureMetrics,
		TLSOpts:       tlsOpts,
	}

	if secureMetrics {
		// FilterProvider is used to protect the metrics endpoint with authn/authz.
		// These configurations ensure that only authorized users and service accounts
		// can access the metrics endpoint. The RBAC are configured in 'config/rbac/kustomization.yaml'. More info:
		// https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.23.0/pkg/metrics/filters#WithAuthenticationAndAuthorization
		metricsServerOptions.FilterProvider = filters.WithAuthenticationAndAuthorization
	}

	// If the certificate is not specified, controller-runtime will automatically
	// generate self-signed certificates for the metrics server. While convenient for development and testing,
	// this setup is not recommended for production.
	//
	// TODO(user): If you enable certManager, uncomment the following lines:
	// - [METRICS-WITH-CERTS] at config/default/kustomization.yaml to generate and use certificates
	// managed by cert-manager for the metrics server.
	// - [PROMETHEUS-WITH-CERTS] at config/prometheus/kustomization.yaml for TLS certification.
	if len(metricsCertPath) > 0 {
		setupLog.Info("Initializing metrics certificate watcher using provided certificates",
			"metrics-cert-path", metricsCertPath, "metrics-cert-name", metricsCertName, "metrics-cert-key", metricsCertKey)

		metricsServerOptions.CertDir = metricsCertPath
		metricsServerOptions.CertName = metricsCertName
		metricsServerOptions.KeyName = metricsCertKey
	}

	// Watch: (WATCH-01) Resync period configuration
	//
	// This manager uses NO explicit SyncPeriod (nil/default), which means controller-runtime
	// does NOT perform periodic reconciliation of all watched resources. This is the CORRECT
	// choice for this operator because:
	//
	// - The controller uses watch events for Pipeline resources, providing real-time updates
	// - The ObservedGeneration pattern handles cache-lag gracefully (see pipeline_controller.go)
	// - Fleet Management collectors poll every 5 minutes independently of this operator
	// - The operator is the sole writer to Fleet Management for these pipelines, so there is
	//   no external state drift detection requirement
	//
	// If periodic resync were needed in the future (e.g., to detect manual Fleet Management
	// changes made outside this operator), it should be set to 10-15 minutes (2-3x the collector
	// poll interval) to avoid unnecessary reconciliation load.
	//
	// Production recommendation: Keep SyncPeriod unset (nil) for watch-driven controllers with
	// no external drift concerns.
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsServerOptions,
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "0fcf8538.grafana.com",
		// LeaderElectionReleaseOnCancel defines if the leader should step down voluntarily
		// when the Manager ends. This requires the binary to immediately end when the
		// Manager is stopped, otherwise, this setting is unsafe. Setting this significantly
		// speeds up voluntary leader transitions as the new leader don't have to wait
		// LeaseDuration time first.
		//
		// In the default scaffold provided, the program ends immediately after
		// the manager stops, so would be fine to enable this option. However,
		// if you are doing or is intended to do any operation such as perform cleanups
		// after the manager stops then its usage might be unsafe.
		// LeaderElectionReleaseOnCancel: true,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Refuse to run with no controllers enabled — that's almost certainly a
	// configuration error and starting an idle manager would be confusing.
	if !enablePipelineController && !enableCollectorController && !enablePolicyController && !enableExternalSyncController {
		setupLog.Error(nil, "no controllers enabled; set at least one --enable-*-controller flag to true")
		os.Exit(1)
	}


	// Initialize Fleet Management API client
	fleetBaseURL := os.Getenv("FLEET_MANAGEMENT_BASE_URL")
	if fleetBaseURL == "" {
		setupLog.Error(nil, "FLEET_MANAGEMENT_BASE_URL environment variable is required")
		os.Exit(1)
	}

	fleetUsername := os.Getenv("FLEET_MANAGEMENT_USERNAME")
	if fleetUsername == "" {
		setupLog.Error(nil, "FLEET_MANAGEMENT_USERNAME environment variable is required")
		os.Exit(1)
	}

	fleetPassword := os.Getenv("FLEET_MANAGEMENT_PASSWORD")
	if fleetPassword == "" {
		setupLog.Error(nil, "FLEET_MANAGEMENT_PASSWORD environment variable is required")
		os.Exit(1)
	}

	setupLog.Info("initializing Fleet Management API client", "baseURL", fleetBaseURL, "username", fleetUsername)
	fleetClient := fleetclient.NewClient(fleetBaseURL, fleetUsername, fleetPassword)

	if enablePipelineController {
		// Cache: mgr.GetClient() returns a cached client backed by the informer cache.
		// All Get() and List() calls through this client read from the in-memory cache, not the API server.
		// This is the controller-runtime default and correct pattern for production controllers.
		if err := (&controller.PipelineReconciler{
			Client:      mgr.GetClient(),
			Scheme:      mgr.GetScheme(),
			FleetClient: fleetClient,
			Recorder:    mgr.GetEventRecorderFor("pipeline-controller"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Pipeline")
			os.Exit(1)
		}

		if err := fleetmanagementv1alpha1.SetupPipelineWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Pipeline")
			os.Exit(1)
		}
	}

	if enableCollectorController {
		if err := (&controller.CollectorReconciler{
			Client:      mgr.GetClient(),
			Scheme:      mgr.GetScheme(),
			FleetClient: fleetClient,
			Recorder:    mgr.GetEventRecorderFor("collector-controller"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "Collector")
			os.Exit(1)
		}

		if err := fleetmanagementv1alpha1.SetupCollectorWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Collector")
			os.Exit(1)
		}
	}

	if enablePolicyController {
		if err := (&controller.RemoteAttributePolicyReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor("policy-controller"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "RemoteAttributePolicy")
			os.Exit(1)
		}

		if err := fleetmanagementv1alpha1.SetupRemoteAttributePolicyWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RemoteAttributePolicy")
			os.Exit(1)
		}
	}

	if enableExternalSyncController {
		if err := (&controller.ExternalAttributeSyncReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: mgr.GetEventRecorderFor("externalattributesync-controller"),
			Factory:  buildExternalSourceFactory(),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ExternalAttributeSync")
			os.Exit(1)
		}

		if err := fleetmanagementv1alpha1.SetupExternalAttributeSyncWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "ExternalAttributeSync")
			os.Exit(1)
		}
	}

	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// buildExternalSourceFactory dispatches by source kind and constructs a
// Source instance. Phase 3 only ships HTTP; SQL returns an error until
// Phase 4 wires it in.
func buildExternalSourceFactory() controller.SourceFactory {
	return func(spec fleetmanagementv1alpha1.ExternalSource, secret *corev1.Secret) (sources.Source, error) {
		switch spec.Kind {
		case fleetmanagementv1alpha1.ExternalSourceKindHTTP:
			if spec.HTTP == nil {
				return nil, fmt.Errorf("ExternalSource kind=HTTP requires spec.source.http")
			}
			cfg := httpsource.Config{
				URL:         spec.HTTP.URL,
				Method:      spec.HTTP.Method,
				RecordsPath: spec.HTTP.RecordsPath,
			}
			if secret != nil {
				cfg.BearerToken = string(secret.Data["bearer-token"])
				cfg.Username = string(secret.Data["username"])
				cfg.Password = string(secret.Data["password"])
			}
			return httpsource.New(cfg)
		case fleetmanagementv1alpha1.ExternalSourceKindSQL:
			if spec.SQL == nil {
				return nil, fmt.Errorf("ExternalSource kind=SQL requires spec.source.sql")
			}
			if secret == nil || len(secret.Data["dsn"]) == 0 {
				return nil, fmt.Errorf(
					"ExternalSource kind=SQL requires secretRef with key %q (DSN connection string)",
					"dsn",
				)
			}
			cfg := sqlsource.Config{
				Driver: spec.SQL.Driver,
				Query:  spec.SQL.Query,
				DSN:    string(secret.Data["dsn"]),
			}
			return sqlsource.New(cfg)
		default:
			return nil, fmt.Errorf("unknown ExternalSource kind %q", spec.Kind)
		}
	}
}
