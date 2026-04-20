package controller

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
	"github.com/example/redis-operator/internal/resources"
)

// buildScheme returns a scheme with all types needed by the controller.
func buildScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	g := NewWithT(t)
	g.Expect(clientgoscheme.AddToScheme(s)).To(Succeed())
	g.Expect(appsv1.AddToScheme(s)).To(Succeed())
	g.Expect(corev1.AddToScheme(s)).To(Succeed())
	g.Expect(redisv1alpha1.AddToScheme(s)).To(Succeed())
	return s
}

// newReconciler returns a RedisReconciler backed by a fake client pre-populated with objs.
func newReconciler(t *testing.T, objs ...runtime.Object) (*RedisReconciler, *fake.ClientBuilder) {
	t.Helper()
	s := buildScheme(t)
	cb := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&redisv1alpha1.Redis{})
	for _, o := range objs {
		cb = cb.WithRuntimeObjects(o)
	}
	r := &RedisReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(16),
	}
	return r, cb
}

func reconcileOnce(t *testing.T, r *RedisReconciler, name, ns string) ctrl.Result {
	t.Helper()
	g := NewWithT(t)
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
	g.Expect(err).NotTo(HaveOccurred())
	return res
}

// ---------- helpers to build minimal Redis CRs ----------

func minimalRedis(name, ns string) *redisv1alpha1.Redis {
	return &redisv1alpha1.Redis{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: redisv1alpha1.RedisSpec{
			Image: "redis:7.2-alpine",
		},
	}
}

func storageRedis(name, ns string) *redisv1alpha1.Redis {
	className := "standard"
	r := minimalRedis(name, ns)
	r.Spec.Storage = &redisv1alpha1.StorageSpec{
		ClassName: &className,
		Size:      "1Gi",
	}
	return r
}

// ---------- tests ----------

func TestReconcile_NoStorage_CreatesDeploymentServiceConfigMap(t *testing.T) {
	g := NewWithT(t)
	r, _ := newReconciler(t, minimalRedis("myredis", "default"))

	// First reconcile adds finalizer and requeues.
	reconcileOnce(t, r, "myredis", "default")
	// Second reconcile creates resources.
	reconcileOnce(t, r, "myredis", "default")

	ctx := context.Background()
	key := types.NamespacedName{Name: "myredis", Namespace: "default"}

	dep := &appsv1.Deployment{}
	g.Expect(r.Get(ctx, key, dep)).To(Succeed())
	g.Expect(dep.Spec.Template.Annotations).To(HaveKey(configHashAnnotation))

	svc := &corev1.Service{}
	g.Expect(r.Get(ctx, key, svc)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myredis-config", Namespace: "default"}, cm)).To(Succeed())
}

func TestReconcile_WithStorage_CreatesStatefulSetHeadlessServiceConfigMap(t *testing.T) {
	g := NewWithT(t)
	r, _ := newReconciler(t, storageRedis("myredis", "default"))

	reconcileOnce(t, r, "myredis", "default")
	reconcileOnce(t, r, "myredis", "default")

	ctx := context.Background()

	sts := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myredis", Namespace: "default"}, sts)).To(Succeed())
	g.Expect(sts.Spec.Template.Annotations).To(HaveKey(configHashAnnotation))

	headless := &corev1.Service{}
	headlessName := resources.HeadlessServiceName("myredis", resources.ComponentRedis)
	g.Expect(r.Get(ctx, types.NamespacedName{Name: headlessName, Namespace: "default"}, headless)).To(Succeed())
	g.Expect(headless.Spec.ClusterIP).To(Equal("None"))

	svc := &corev1.Service{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myredis", Namespace: "default"}, svc)).To(Succeed())

	cm := &corev1.ConfigMap{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myredis-config", Namespace: "default"}, cm)).To(Succeed())
}

func TestReconcile_FinalizerIsAdded(t *testing.T) {
	g := NewWithT(t)
	r, _ := newReconciler(t, minimalRedis("myredis", "default"))

	reconcileOnce(t, r, "myredis", "default")

	updated := &redisv1alpha1.Redis{}
	g.Expect(r.Get(context.Background(), types.NamespacedName{Name: "myredis", Namespace: "default"}, updated)).To(Succeed())
	g.Expect(updated.Finalizers).To(ContainElement(redisv1alpha1.FinalizerName))
}

func TestReconcile_ConfigUpdate_ChangesPodTemplateHash(t *testing.T) {
	g := NewWithT(t)
	redisObj := minimalRedis("myredis", "default")
	r, _ := newReconciler(t, redisObj)

	// Reconcile twice to get past finalizer add.
	reconcileOnce(t, r, "myredis", "default")
	reconcileOnce(t, r, "myredis", "default")

	// Record initial config hash.
	dep := &appsv1.Deployment{}
	ctx := context.Background()
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myredis", Namespace: "default"}, dep)).To(Succeed())
	hash1 := dep.Spec.Template.Annotations[configHashAnnotation]

	// Update the Redis spec to add a config key.
	updated := &redisv1alpha1.Redis{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myredis", Namespace: "default"}, updated)).To(Succeed())
	updated.Spec.RedisConfig = map[string]string{"maxmemory": "256mb"}
	g.Expect(r.Update(ctx, updated)).To(Succeed())

	// Reconcile again — ConfigMap and pod template hash should change.
	reconcileOnce(t, r, "myredis", "default")

	dep2 := &appsv1.Deployment{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "myredis", Namespace: "default"}, dep2)).To(Succeed())
	hash2 := dep2.Spec.Template.Annotations[configHashAnnotation]

	g.Expect(hash2).NotTo(Equal(hash1), "config hash should change after config update")
}

func TestReconcile_Delete_RemovesFinalizer(t *testing.T) {
	g := NewWithT(t)
	redisObj := minimalRedis("myredis", "default")
	// Pre-seed the finalizer to skip past the add step.
	redisObj.Finalizers = []string{redisv1alpha1.FinalizerName}
	now := metav1.Now()
	redisObj.DeletionTimestamp = &now
	r, _ := newReconciler(t, redisObj)

	reconcileOnce(t, r, "myredis", "default")

	remaining := &redisv1alpha1.Redis{}
	err := r.Get(context.Background(), types.NamespacedName{Name: "myredis", Namespace: "default"}, remaining)
	// Object is deleted OR finalizer is removed.
	if err == nil {
		g.Expect(remaining.Finalizers).NotTo(ContainElement(redisv1alpha1.FinalizerName))
	}
}

func TestReconcile_Delete_WithStorage_DeletesPVC(t *testing.T) {
	g := NewWithT(t)
	redisObj := storageRedis("myredis", "default")
	redisObj.Finalizers = []string{redisv1alpha1.FinalizerName}
	now := metav1.Now()
	redisObj.DeletionTimestamp = &now

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-myredis-0", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}

	r, _ := newReconciler(t, redisObj, pvc)
	reconcileOnce(t, r, "myredis", "default")

	remaining := &corev1.PersistentVolumeClaim{}
	err := r.Get(context.Background(), types.NamespacedName{Name: "data-myredis-0", Namespace: "default"}, remaining)
	g.Expect(err).To(HaveOccurred(), "PVC should have been deleted")
}

func TestReconcile_Delete_WithStorage_KeepAfterDeletion(t *testing.T) {
	g := NewWithT(t)
	redisObj := storageRedis("myredis", "default")
	redisObj.Spec.Storage.KeepAfterDeletion = true
	redisObj.Finalizers = []string{redisv1alpha1.FinalizerName}
	now := metav1.Now()
	redisObj.DeletionTimestamp = &now

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-myredis-0", Namespace: "default"},
	}

	r, _ := newReconciler(t, redisObj, pvc)
	reconcileOnce(t, r, "myredis", "default")

	remaining := &corev1.PersistentVolumeClaim{}
	err := r.Get(context.Background(), types.NamespacedName{Name: "data-myredis-0", Namespace: "default"}, remaining)
	g.Expect(err).NotTo(HaveOccurred(), "PVC should be preserved when keepAfterDeletion=true")
}

func TestConfigDataHash_DeterministicAndSensitive(t *testing.T) {
	g := NewWithT(t)

	data1 := map[string]string{"maxmemory": "100mb", "hz": "10"}
	data2 := map[string]string{"hz": "10", "maxmemory": "100mb"}
	data3 := map[string]string{"maxmemory": "200mb", "hz": "10"}

	h1 := configDataHash(data1)
	h2 := configDataHash(data2)
	h3 := configDataHash(data3)

	g.Expect(h1).To(Equal(h2), "same keys/values in different order should produce same hash")
	g.Expect(h1).NotTo(Equal(h3), "different values should produce different hash")
	g.Expect(h1).To(HaveLen(16))
}
