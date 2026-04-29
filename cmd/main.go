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
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	ctrlmanager "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace/noop"
	corev1 "k8s.io/api/core/v1"

	fleetmanagementv1alpha1 "github.com/grafana/fleet-management-operator/api/v1alpha1"
	"github.com/grafana/fleet-management-operator/internal/controller"
	"github.com/grafana/fleet-management-operator/internal/tenant"
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
	var webhookCertPath, webhookCertName, webhookCertKey string
	var webhookPort int
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
	var enableCollectorDiscoveryController bool
	var enablePipelineDiscoveryController bool
	var enableTenantPolicyEnforcement bool
	var fleetAPIRPS float64
	var fleetAPIBurst int
	var policyMaxConcurrent int
	var syncMaxConcurrent int
	var discoveryMaxConcurrent int
	var syncSourceTargetRate float64
	var syncSourceTargetBurst int
	flag.StringVar(&metricsAddr, "metrics-bind-address", "0", "The address the metrics endpoint binds to. "+
		"Use :8443 for HTTPS or :8080 for HTTP, or leave as 0 to disable the metrics service.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", true,
		"If set, the metrics endpoint is served securely via HTTPS. Use --metrics-secure=false to use HTTP instead.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "The port that the webhook server listens on.")
	flag.StringVar(&webhookCertPath, "webhook-cert-path", "", "The directory that contains the webhook certificate.")
	flag.StringVar(&webhookCertName, "webhook-cert-name", "tls.crt", "The name of the webhook certificate file.")
	flag.StringVar(&webhookCertKey, "webhook-cert-key", "tls.key", "The name of the webhook key file.")
	// NOTE: metrics-cert-{path,name,key} flags were intentionally dropped — the
	// chart does not ship TLS material to the metrics endpoint, and
	// controller-runtime auto-generates a self-signed cert when --metrics-secure
	// is on (sufficient for in-cluster Prometheus scraping). Re-introduce these
	// flags if/when the chart starts mounting customer-managed metrics certs.
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
	flag.BoolVar(&enableCollectorDiscoveryController, "enable-collector-discovery-controller", false,
		"Enable the CollectorDiscovery reconciler and webhook (auto-mirrors Fleet Management collectors as Collector CRs).")
	flag.BoolVar(&enablePipelineDiscoveryController, "enable-pipeline-discovery-controller", false,
		"Enable the PipelineDiscovery controller (polls Fleet Management ListPipelines and creates Pipeline CRs).")
	flag.BoolVar(&enableTenantPolicyEnforcement, "enable-tenant-policy-enforcement", false,
		"Enable TenantPolicy CRD validation and enforcement. When set, validating webhooks for "+
			"Pipeline, RemoteAttributePolicy, and ExternalAttributeSync require that K8s subjects "+
			"matched by a TenantPolicy include at least one of the policy's required matchers in "+
			"the CR's matcher set. Default false; existing installs see no behavior change until "+
			"this flag is set.")
	flag.Float64Var(&fleetAPIRPS, "fleet-api-rps", 3,
		"Fleet Management API sustained rate limit in requests per second. "+
			"Match this to your Fleet Management server-side api: rate setting. "+
			"The standard stack default is 3; large or custom deployments may be higher.")
	flag.IntVar(&fleetAPIBurst, "fleet-api-burst", 50,
		"Fleet Management API rate-limiter burst size. Absorbs startup and post-restart "+
			"request spikes without changing the sustained RPS ceiling. "+
			"burst=1 causes livelock at scale: request #(rps*30+1) in a restart wave "+
			"waits 30s and hits the HTTP timeout, indistinguishable from API outage.")
	flag.IntVar(&policyMaxConcurrent, "controller-policy-max-concurrent", 4,
		"Max concurrent reconciles for RemoteAttributePolicy. Safe to increase: reconciles "+
			"are pure K8s cache reads with no external API calls. Pipeline and Collector "+
			"must stay at 1 because they share the Fleet API rate budget.")
	flag.IntVar(&syncMaxConcurrent, "controller-sync-max-concurrent", 4,
		"Max concurrent reconciles for ExternalAttributeSync. Safe to increase: Fetch "+
			"calls are per-source and do not share external state across reconciles.")
	flag.IntVar(&discoveryMaxConcurrent, "controller-discovery-max-concurrent", 1,
		"Max concurrent reconciles for CollectorDiscovery. Keep at 1: concurrency > 1 "+
			"triggers multiple ListCollectors calls per poll cycle without benefit.")
	flag.Float64Var(&syncSourceTargetRate, "controller-sync-target-rate", 0,
		"Per-target rate limit (tokens/sec) applied before each ExternalAttributeSync Source.Fetch "+
			"call. Two syncs against the same upstream (HTTP host or SQL secret) share a token "+
			"bucket so MaxConcurrentReconciles cannot stampede a customer-owned source. Zero (default) "+
			"disables per-target limiting. Set 1 for one fetch/sec/upstream — typically plenty given "+
			"that sync schedules run every minute or longer.")
	flag.IntVar(&syncSourceTargetBurst, "controller-sync-target-burst", 4,
		"Bucket size for the per-target ExternalAttributeSync limiter. Ignored when "+
			"--controller-sync-target-rate=0. Default 4 (matching --controller-sync-max-concurrent so "+
			"a single concurrency generation always passes through immediately).")
	var leaderElectionLeaseDuration time.Duration
	var leaderElectionRenewDeadline time.Duration
	var leaderElectionRetryPeriod time.Duration

	flag.DurationVar(&leaderElectionLeaseDuration, "leader-election-lease-duration", 15*time.Second,
		"Duration non-leader candidates will wait before forcing leader acquisition.")
	flag.DurationVar(&leaderElectionRenewDeadline, "leader-election-renew-deadline", 10*time.Second,
		"Duration the acting leader will retry refreshing leadership before giving up.")
	flag.DurationVar(&leaderElectionRetryPeriod, "leader-election-retry-period", 2*time.Second,
		"Duration leader-election clients wait between action attempts.")
	opts := zap.Options{
		Development: false,
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
		Port:    webhookPort,
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

	// Metrics endpoint TLS material is intentionally NOT wired through the
	// chart today. When --metrics-secure is on, controller-runtime
	// auto-generates a per-pod self-signed cert; that is sufficient for
	// in-cluster Prometheus scraping with the default
	// FilterProvider=WithAuthenticationAndAuthorization (RBAC-gated). If
	// customer-managed metrics certs are required in the future, reintroduce
	// --metrics-cert-{path,name,key} together with a chart-level cert mount.

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
		Scheme:                        scheme,
		Metrics:                       metricsServerOptions,
		WebhookServer:                 webhookServer,
		HealthProbeBindAddress:        probeAddr,
		LeaderElection:                enableLeaderElection,
		LeaderElectionID:              "0fcf8538.grafana.com",
		LeaderElectionReleaseOnCancel: true,
		LeaseDuration:                 &leaderElectionLeaseDuration,
		RenewDeadline:                 &leaderElectionRenewDeadline,
		RetryPeriod:                   &leaderElectionRetryPeriod,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	setupLog.Info("webhook server configured", "port", webhookPort)

	// Refuse to run with no controllers enabled — that's almost certainly a
	// configuration error and starting an idle manager would be confusing.
	if !enablePipelineController &&
		!enableCollectorController &&
		!enablePolicyController &&
		!enableExternalSyncController &&
		!enableCollectorDiscoveryController &&
		!enablePipelineDiscoveryController {
		setupLog.Error(nil, "no controllers enabled; set at least one --enable-*-controller flag to true")
		os.Exit(1)
	}

	// Discovery without the Collector reconciler is a misconfiguration:
	// discovery would create Collector CRs that nobody acts on. Fail
	// fast instead of silently leaking objects.
	if enableCollectorDiscoveryController && !enableCollectorController {
		setupLog.Error(nil,
			"--enable-collector-discovery-controller requires --enable-collector-controller; "+
				"discovery without a Collector reconciler creates CRs that no controller acts on")
		os.Exit(1)
	}

	// OpenTelemetry tracing — noop by default, enabled when OTEL_EXPORTER_OTLP_ENDPOINT is set.
	// Setting up tracing here (after manager creation) so the manager shutdown hook can
	// flush any pending spans before the process exits.
	tracer := noop.NewTracerProvider().Tracer("fleet-management-operator")

	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		// Construct the exporter without explicit options so the SDK can read
		// the standard OTEL_EXPORTER_OTLP_* environment variables itself
		// (ENDPOINT, TRACES_ENDPOINT, INSECURE, HEADERS, CERTIFICATE, etc.).
		// Forcing WithInsecure / WithEndpoint here would override valid TLS or
		// header configuration from the environment.
		exp, otelErr := otlptracegrpc.New(context.Background())
		if otelErr != nil {
			setupLog.Error(otelErr, "failed to create OTEL exporter; tracing disabled")
		} else {
			// sdkresource.New may return a partial resource AND a non-nil
			// error when one of the detectors fails (e.g. cannot read a
			// container env var). Log the error but continue: tracing is
			// opt-in and a resource-detection failure should never crash
			// the manager. Pass whatever resource we got — `res` is the
			// merged successful detectors when err != nil; `nil` is a
			// valid argument to WithResource and falls back to the SDK
			// default resource.
			res, resErr := sdkresource.New(context.Background(),
				sdkresource.WithAttributes(
					// "service.name" is the canonical OTEL resource attribute key
					// (go.opentelemetry.io/otel/semconv/v1.27.0.ServiceNameKey).
					attribute.String("service.name", "fleet-management-operator"),
				),
			)
			if resErr != nil {
				setupLog.Error(resErr, "OTEL resource detection partially failed; "+
					"tracing continues with whatever attributes were collected")
			}
			tp := sdktrace.NewTracerProvider(
				sdktrace.WithBatcher(exp),
				sdktrace.WithResource(res),
			)
			otel.SetTracerProvider(tp)
			tracer = tp.Tracer("fleet-management-operator")
			if addErr := mgr.Add(ctrlmanager.RunnableFunc(func(ctx context.Context) error {
				<-ctx.Done()
				// Bound the shutdown so a stuck OTLP collector cannot
				// indefinitely delay process exit. 5s is generous for the
				// batcher to flush its queue while still letting K8s
				// gracefully terminate within the default 30s grace period.
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := tp.Shutdown(shutdownCtx); err != nil {
					setupLog.Error(err, "tracer provider shutdown failed")
				}
				return nil
			})); addErr != nil {
				setupLog.Error(addErr, "unable to register OTEL shutdown hook")
			}
			setupLog.Info("OpenTelemetry tracing enabled", "endpoint", endpoint)
		}
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

	setupLog.Info("initializing Fleet Management API client",
		"baseURL", fleetBaseURL, "username", fleetUsername,
		"rps", fleetAPIRPS, "burst", fleetAPIBurst)
	fleetClient := fleetclient.NewClient(fleetBaseURL, fleetUsername, fleetPassword,
		fleetclient.WithRateLimit(fleetAPIRPS, fleetAPIBurst),
		fleetclient.WithTracer(tracer))

	// Two-tier rate limiting:
	//   Tier 1 — workqueue (controller-runtime default): 10 qps bucket, burst 100.
	//     Bounds the reconcile-dispatch rate so the K8s API server is not flooded
	//     during batch events (rolling deploy, mass delete, startup cache warm-up).
	//   Tier 2 — Fleet API client (--fleet-api-rps / --fleet-api-burst):
	//     Enforces compliance with the Fleet Management server-side api: rate budget.
	//
	// At steady state, up to 10 reconciles/s enter the workqueue; reconciles that
	// require a Fleet API call wait at limiter.Wait(ctx) in the client interceptor,
	// holding one goroutine each. This is the correct design: Tier 1 prevents K8s
	// API storms; Tier 2 is the true Fleet throughput ceiling. Raising
	// --fleet-api-rps above the server-side limit produces 429s that are
	// indistinguishable from outages. Do not configure --fleet-api-rps above the
	// Fleet Management server-side api: setting for this stack.
	//
	// Controllers that do NOT call the Fleet API (Policy, ExternalSync, Discovery)
	// are unaffected by Tier 2; their throughput is bounded only by the workqueue
	// and by --controller-{policy,sync,discovery}-max-concurrent.

	// TenantPolicy status reconciler runs unconditionally. The TenantPolicy
	// CRD is always installed by the chart, so users can apply
	// TenantPolicy CRs whether or not enforcement is on; the reconciler
	// gives them in-cluster Ready/Valid feedback in either case. It is
	// local-only — no Fleet API calls, no finalizer — so the cost is
	// negligible.
	if err := (&controller.TenantPolicyReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("tenantpolicy-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TenantPolicy")
		os.Exit(1)
	}

	// The TenantPolicy admission webhook validates the CR shape itself —
	// matcher syntax, namespace selector parse, empty-selector warnings —
	// and does NOT depend on the enforcement checker (the validator type in
	// api/v1alpha1 has no checker dependency; only the OTHER CRs' webhooks
	// consult `tenantChecker`). Register it unconditionally so that an
	// install that has the TenantPolicy CRD installed (chart key
	// `controllers.tenantPolicy.enabled: true`) but enforcement turned off
	// still gets API-server-side validation when users `kubectl apply`
	// TenantPolicy resources.
	if err := fleetmanagementv1alpha1.SetupTenantPolicyWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "TenantPolicy")
		os.Exit(1)
	}

	// Tenant policy enforcement is opt-in and default-off. When disabled,
	// tenantChecker stays nil and the consuming webhooks (Pipeline /
	// RemoteAttributePolicy / ExternalAttributeSync) behave identically to
	// a build without this feature. When enabled, the checker reads
	// TenantPolicy resources via the manager's cached client at admission
	// time. This gate is independent of the TenantPolicy webhook
	// registration above: users may install the CRD and author policies
	// (with shape validation) before flipping enforcement on.
	var tenantChecker fleetmanagementv1alpha1.MatcherChecker
	if enableTenantPolicyEnforcement {
		setupLog.Info("tenant policy enforcement enabled; webhooks will consult TenantPolicy resources")
		tenantChecker = tenant.NewChecker(mgr.GetClient())
	}

	// fleetClient.Close() is best-effort. controller-runtime cancels every
	// Runnable's context simultaneously on shutdown, so this hook races
	// with in-flight reconcilers that may still hold the client. Close()
	// only releases idle HTTP connections; in-flight Fleet API calls are
	// not cancelled here — they complete on their own, bounded by the
	// 30s HTTP client timeout. We deliberately do NOT add a WaitGroup or
	// graceful drain: the marginal benefit (slightly faster idle conn
	// release at process exit) does not justify the added coordination
	// surface, and Kubernetes' default 30s pod terminationGracePeriod
	// already accommodates any in-flight call hitting the timeout.
	if err := mgr.Add(ctrlmanager.RunnableFunc(func(ctx context.Context) error {
		<-ctx.Done()
		fleetClient.Close()
		return nil
	})); err != nil {
		setupLog.Error(err, "unable to register fleet client shutdown hook")
		os.Exit(1)
	}

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

		if err := fleetmanagementv1alpha1.SetupPipelineWebhookWithManager(mgr, tenantChecker); err != nil {
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
			Client:                  mgr.GetClient(),
			Scheme:                  mgr.GetScheme(),
			Recorder:                mgr.GetEventRecorderFor("policy-controller"),
			MaxConcurrentReconciles: policyMaxConcurrent,
		}).SetupWithManager(context.Background(), mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "RemoteAttributePolicy")
			os.Exit(1)
		}

		if err := fleetmanagementv1alpha1.SetupRemoteAttributePolicyWebhookWithManager(mgr, tenantChecker); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RemoteAttributePolicy")
			os.Exit(1)
		}
	}

	if enableExternalSyncController {
		if err := (&controller.ExternalAttributeSyncReconciler{
			Client:                  mgr.GetClient(),
			Scheme:                  mgr.GetScheme(),
			Recorder:                mgr.GetEventRecorderFor("externalattributesync-controller"),
			Factory:                 buildExternalSourceFactory(),
			MaxConcurrentReconciles: syncMaxConcurrent,
			SourceTargetRate:        syncSourceTargetRate,
			SourceTargetBurst:       syncSourceTargetBurst,
		}).SetupWithManager(context.Background(), mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "ExternalAttributeSync")
			os.Exit(1)
		}

		if err := fleetmanagementv1alpha1.SetupExternalAttributeSyncWebhookWithManager(mgr, tenantChecker); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "ExternalAttributeSync")
			os.Exit(1)
		}
	}

	if enableCollectorDiscoveryController {
		if err := (&controller.CollectorDiscoveryReconciler{
			Client:                  mgr.GetClient(),
			Scheme:                  mgr.GetScheme(),
			FleetClient:             fleetClient,
			Recorder:                mgr.GetEventRecorderFor("collectordiscovery-controller"),
			MaxConcurrentReconciles: discoveryMaxConcurrent,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "CollectorDiscovery")
			os.Exit(1)
		}

		if err := fleetmanagementv1alpha1.SetupCollectorDiscoveryWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "CollectorDiscovery")
			os.Exit(1)
		}
	}

	if enablePipelineDiscoveryController {
		if err = (&controller.PipelineDiscoveryReconciler{
			Client:      mgr.GetClient(),
			Scheme:      mgr.GetScheme(),
			FleetClient: fleetClient,
			Recorder:    mgr.GetEventRecorderFor("pipeline-discovery-controller"),
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "PipelineDiscovery")
			os.Exit(1)
		}
	}

	if err = (&fleetmanagementv1alpha1.PipelineDiscoveryValidator{}).SetupWebhookWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create webhook", "webhook", "PipelineDiscovery")
		os.Exit(1)
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

	if webhookCertPath != "" {
		// Stat the directory and both expected files, not just the
		// directory: an empty cert directory passes os.Stat but the
		// webhook server would fail later when controller-runtime tries
		// to read tls.crt / tls.key. Failing fast here gives a clear
		// error pointing at the missing file rather than a generic
		// startup failure deeper in the manager.
		if _, statErr := os.Stat(webhookCertPath); statErr != nil {
			setupLog.Error(statErr, "webhook cert path not accessible", "path", webhookCertPath)
			os.Exit(1)
		}
		certFile := filepath.Join(webhookCertPath, webhookCertName)
		if _, statErr := os.Stat(certFile); statErr != nil {
			setupLog.Error(statErr, "webhook TLS cert file not accessible", "file", certFile)
			os.Exit(1)
		}
		keyFile := filepath.Join(webhookCertPath, webhookCertKey)
		if _, statErr := os.Stat(keyFile); statErr != nil {
			setupLog.Error(statErr, "webhook TLS key file not accessible", "file", keyFile)
			os.Exit(1)
		}
		setupLog.Info("webhook TLS cert path verified",
			"path", webhookCertPath, "cert", webhookCertName, "key", webhookCertKey)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// buildExternalSourceFactory dispatches by source kind and constructs a
// Source instance. HTTP and SQL (postgres/mysql via lib/pq + go-sql-driver)
// are both wired; new source kinds are added by extending the switch and
// registering the matching driver / package.
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
