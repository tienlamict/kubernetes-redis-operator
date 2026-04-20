package main

import (
	"flag"
	"os"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
	"github.com/example/redis-operator/internal/controller"
	redisclient "github.com/example/redis-operator/internal/redis"
	// Import metrics package to register metrics via init().
	_ "github.com/example/redis-operator/internal/metrics"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(redisv1alpha1.AddToScheme(scheme))
	utilruntime.Must(monitoringv1.AddToScheme(scheme))

	// Register additional Kubernetes types used by the operator.
	utilruntime.Must(appsv1.AddToScheme(scheme))
	utilruntime.Must(corev1.AddToScheme(scheme))
	utilruntime.Must(policyv1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var webhookPort int
	var enableWebhooks bool

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the health probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election for high availability.")
	flag.IntVar(&webhookPort, "webhook-port", 9443, "Port for the webhook server.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", false, "Enable admission webhooks (requires cert setup).")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		WebhookServer:          webhook.NewServer(webhook.Options{Port: webhookPort}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "redis-operator.example.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Build Redis client with default options.
	redisOpts := redisclient.DefaultClientOptions()
	rc := redisclient.NewClient(redisOpts)
	cm := redisclient.NewClusterManager(rc)

	// Register controllers.
	if err := (&controller.RedisReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Redis")
		os.Exit(1)
	}

	if err := (&controller.RedisSentinelReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RedisSentinel")
		os.Exit(1)
	}

	if err := (&controller.RedisClusterReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		RedisClient:    rc,
		ClusterManager: cm,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "RedisCluster")
		os.Exit(1)
	}

	// Register admission webhooks (requires cert-manager or manual cert setup).
	if enableWebhooks {
		if err := (&redisv1alpha1.Redis{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "Redis")
			os.Exit(1)
		}
		if err := (&redisv1alpha1.RedisSentinel{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RedisSentinel")
			os.Exit(1)
		}
		if err := (&redisv1alpha1.RedisCluster{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "RedisCluster")
			os.Exit(1)
		}
	}

	// Register health probes.
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
