package controller

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
	"github.com/example/redis-operator/internal/resources"
)

// sentinelReconciler returns a reconciler seeded with the provided object.
func sentinelReconciler(t *testing.T, rs *redisv1alpha1.RedisSentinel) *RedisSentinelReconciler {
	t.Helper()
	s := buildScheme(t)
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&redisv1alpha1.RedisSentinel{}).
		WithObjects(rs)
	return &RedisSentinelReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(16),
	}
}

func reconcileSentinelOnce(t *testing.T, r *RedisSentinelReconciler, name, ns string) ctrl.Result {
	t.Helper()
	g := NewWithT(t)
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
	g.Expect(err).NotTo(HaveOccurred())
	return res
}

// ---------- helpers ----------

func minimalSentinel(name, ns string) *redisv1alpha1.RedisSentinel {
	return &redisv1alpha1.RedisSentinel{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: redisv1alpha1.RedisSentinelSpec{
			Image:            "redis:7.2-alpine",
			Replicas:         3,
			SentinelReplicas: 3,
		},
	}
}

// ---------- tests ----------

func TestSentinelReconcile_AddsFinalizerOnFirstReconcile(t *testing.T) {
	g := NewWithT(t)
	rs := minimalSentinel("myha", "default")
	r := sentinelReconciler(t, rs)

	reconcileSentinelOnce(t, r, "myha", "default")

	updated := &redisv1alpha1.RedisSentinel{}
	g.Expect(r.Get(context.Background(), types.NamespacedName{Name: "myha", Namespace: "default"}, updated)).To(Succeed())
	g.Expect(updated.Finalizers).To(ContainElement(redisv1alpha1.FinalizerName))
}

func TestSentinelReconcile_CreatesAllCoreResources(t *testing.T) {
	g := NewWithT(t)
	rs := minimalSentinel("myha", "default")
	r := sentinelReconciler(t, rs)

	// First reconcile: add finalizer + requeue.
	reconcileSentinelOnce(t, r, "myha", "default")
	// Second reconcile: create resources.
	reconcileSentinelOnce(t, r, "myha", "default")

	ctx := context.Background()

	// Redis ConfigMap
	redisCM := &corev1.ConfigMap{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha-config", Namespace: "default"}, redisCM)).To(Succeed())

	// Sentinel ConfigMap
	sentinelCM := &corev1.ConfigMap{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha-sentinel-config", Namespace: "default"}, sentinelCM)).To(Succeed())

	// Redis StatefulSet
	sts := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha", Namespace: "default"}, sts)).To(Succeed())
	g.Expect(sts.Spec.Template.Annotations).To(HaveKey(configHashAnnotation))

	// Sentinel Deployment
	dep := &appsv1.Deployment{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha-sentinel", Namespace: "default"}, dep)).To(Succeed())

	// Headless Service for Redis StatefulSet
	headless := &corev1.Service{}
	headlessName := resources.HeadlessServiceName("myha", resources.ComponentRedis)
	g.Expect(r.Get(ctx, types.NamespacedName{Name: headlessName, Namespace: "default"}, headless)).To(Succeed())
	g.Expect(headless.Spec.ClusterIP).To(Equal("None"))

	// Master Service
	masterSvc := &corev1.Service{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha-master", Namespace: "default"}, masterSvc)).To(Succeed())
	g.Expect(masterSvc.Spec.Selector).To(HaveKeyWithValue(resources.RoleLabelKey, resources.RoleMaster))

	// Replica Service
	replicaSvc := &corev1.Service{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha-replica", Namespace: "default"}, replicaSvc)).To(Succeed())
	g.Expect(replicaSvc.Spec.Selector).To(HaveKeyWithValue(resources.RoleLabelKey, resources.RoleReplica))

	// Sentinel Service
	sentinelSvc := &corev1.Service{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha-sentinel", Namespace: "default"}, sentinelSvc)).To(Succeed())
}

func TestSentinelReconcile_ConfigHashInjectedInStatefulSet(t *testing.T) {
	g := NewWithT(t)
	rs := minimalSentinel("myha", "default")
	r := sentinelReconciler(t, rs)

	reconcileSentinelOnce(t, r, "myha", "default") // adds finalizer
	reconcileSentinelOnce(t, r, "myha", "default") // creates resources

	sts := &appsv1.StatefulSet{}
	g.Expect(r.Get(context.Background(), types.NamespacedName{Name: "myha", Namespace: "default"}, sts)).To(Succeed())
	hash := sts.Spec.Template.Annotations[configHashAnnotation]
	g.Expect(hash).To(HaveLen(16))
}

func TestSentinelReconcile_ConfigUpdateChangesHash(t *testing.T) {
	g := NewWithT(t)
	rs := minimalSentinel("myha", "default")
	r := sentinelReconciler(t, rs)

	reconcileSentinelOnce(t, r, "myha", "default")
	reconcileSentinelOnce(t, r, "myha", "default")

	ctx := context.Background()

	sts := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha", Namespace: "default"}, sts)).To(Succeed())
	hash1 := sts.Spec.Template.Annotations[configHashAnnotation]

	updated := &redisv1alpha1.RedisSentinel{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha", Namespace: "default"}, updated)).To(Succeed())
	updated.Spec.RedisConfig = map[string]string{"maxmemory": "512mb"}
	g.Expect(r.Update(ctx, updated)).To(Succeed())

	reconcileSentinelOnce(t, r, "myha", "default")

	sts2 := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myha", Namespace: "default"}, sts2)).To(Succeed())
	hash2 := sts2.Spec.Template.Annotations[configHashAnnotation]

	g.Expect(hash2).NotTo(Equal(hash1), "config hash must change when redisConfig changes")
}

func TestSentinelReconcile_Delete_RemovesFinalizer(t *testing.T) {
	g := NewWithT(t)
	rs := minimalSentinel("myha", "default")
	rs.Finalizers = []string{redisv1alpha1.FinalizerName}
	now := metav1.Now()
	rs.DeletionTimestamp = &now

	r := sentinelReconciler(t, rs)
	reconcileSentinelOnce(t, r, "myha", "default")

	remaining := &redisv1alpha1.RedisSentinel{}
	err := r.Get(context.Background(), types.NamespacedName{Name: "myha", Namespace: "default"}, remaining)
	if err == nil {
		g.Expect(remaining.Finalizers).NotTo(ContainElement(redisv1alpha1.FinalizerName))
	}
}

func TestSentinelReconcile_MasterServiceSelector_UsesRoleLabel(t *testing.T) {
	g := NewWithT(t)
	rs := minimalSentinel("myha", "default")
	r := sentinelReconciler(t, rs)

	reconcileSentinelOnce(t, r, "myha", "default")
	reconcileSentinelOnce(t, r, "myha", "default")

	masterSvc := &corev1.Service{}
	g.Expect(r.Get(context.Background(),
		types.NamespacedName{Name: "myha-master", Namespace: "default"}, masterSvc)).To(Succeed())

	g.Expect(masterSvc.Spec.Selector).To(HaveKeyWithValue(resources.RoleLabelKey, resources.RoleMaster),
		"master service must select pods by role label, not by component label")
}

func TestSentinelReconcile_ReplicaServiceSelector_UsesRoleLabel(t *testing.T) {
	g := NewWithT(t)
	rs := minimalSentinel("myha", "default")
	r := sentinelReconciler(t, rs)

	reconcileSentinelOnce(t, r, "myha", "default")
	reconcileSentinelOnce(t, r, "myha", "default")

	replicaSvc := &corev1.Service{}
	g.Expect(r.Get(context.Background(),
		types.NamespacedName{Name: "myha-replica", Namespace: "default"}, replicaSvc)).To(Succeed())

	g.Expect(replicaSvc.Spec.Selector).To(HaveKeyWithValue(resources.RoleLabelKey, resources.RoleReplica),
		"replica service must select pods by role label")
}

func TestSentinelReconcile_RoleLabels_FallbackToPodZero(t *testing.T) {
	g := NewWithT(t)

	// Seed a fake pod to simulate a running Redis pod.
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myha-0",
			Namespace: "default",
			Labels:    resources.PodLabels("myha", resources.ComponentRedis),
		},
		Status: corev1.PodStatus{PodIP: "10.0.0.1"},
	}

	s := buildScheme(t)
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&redisv1alpha1.RedisSentinel{}).
		WithObjects(minimalSentinel("myha", "default"), pod)
	r := &RedisSentinelReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(16),
		// RedisClient is nil → fallback to pod-0 as master
	}

	reconcileSentinelOnce(t, r, "myha", "default")
	reconcileSentinelOnce(t, r, "myha", "default")

	// pod-0 should be labelled as master.
	patchedPod := &corev1.Pod{}
	g.Expect(r.Get(context.Background(), types.NamespacedName{Name: "myha-0", Namespace: "default"}, patchedPod)).To(Succeed())
	g.Expect(patchedPod.Labels[resources.RoleLabelKey]).To(Equal(resources.RoleMaster))
}
