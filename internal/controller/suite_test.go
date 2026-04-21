package controller

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

var (
	k8sClient  client.Client
	testEnv    *envtest.Environment
	testCtx    context.Context
	testCancel context.CancelFunc
)

// TestControllers is the Ginkgo entry point for the controller suite.
func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	testCtx, testCancel = context.WithCancel(context.Background())

	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{"../../config/crd/bases"},
		ErrorIfCRDPathMissing: true,
	}

	// Resolve kubebuilder asset binaries. Prefer the KUBEBUILDER_ASSETS env var;
	// fall back to querying setup-envtest so the suite works both in CI (with the
	// env var set by the Makefile) and locally (after running `make envtest`).
	if assetsDir := resolveEnvtestAssets(); assetsDir != "" {
		testEnv.BinaryAssetsDirectory = assetsDir
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	scheme := buildEnvtestScheme()

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme})
	Expect(err).NotTo(HaveOccurred())

	// Register all three controllers so their watch loops run during envtest specs.
	Expect((&RedisReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("redis-controller"),
	}).SetupWithManager(mgr)).To(Succeed())

	Expect((&RedisSentinelReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("redissentinel-controller"),
		// RedisClient left nil: Sentinel role-label reconciliation falls back to pod-0.
	}).SetupWithManager(mgr)).To(Succeed())

	Expect((&RedisClusterReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("rediscluster-controller"),
		// ClusterManager left nil: lifecycle is guarded; resource creation still runs.
	}).SetupWithManager(mgr)).To(Succeed())

	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(testCtx)).To(Succeed())
	}()
})

var _ = AfterSuite(func() {
	testCancel()
	By("tearing down the test environment")
	// testEnv.Stop() may return a "not supported by windows" error when it
	// tries to send SIGTERM to kube-apiserver/etcd on Windows.  The processes
	// are still cleaned up by the OS when the test binary exits, so we treat
	// the Windows signal error as non-fatal.
	if err := testEnv.Stop(); err != nil {
		if !strings.Contains(err.Error(), "not supported by windows") {
			Expect(err).NotTo(HaveOccurred())
		}
	}
})

// buildEnvtestScheme returns a scheme that includes all types used by the
// three controllers. Registering monitoring types is best-effort.
func buildEnvtestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	Expect(clientgoscheme.AddToScheme(s)).To(Succeed())
	Expect(appsv1.AddToScheme(s)).To(Succeed())
	Expect(corev1.AddToScheme(s)).To(Succeed())
	Expect(policyv1.AddToScheme(s)).To(Succeed())
	Expect(redisv1alpha1.AddToScheme(s)).To(Succeed())
	// ServiceMonitor is optional; ignore if the CRD is absent.
	_ = monitoringv1.AddToScheme(s)
	return s
}

// resolveEnvtestAssets returns the directory containing the kubebuilder
// binary assets (kube-apiserver, etcd). It first checks KUBEBUILDER_ASSETS;
// if unset it falls back to querying setup-envtest directly.
func resolveEnvtestAssets() string {
	if dir := os.Getenv("KUBEBUILDER_ASSETS"); dir != "" {
		return dir
	}
	out, err := exec.Command("setup-envtest", "use", "1.31.0", "-p", "path").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
