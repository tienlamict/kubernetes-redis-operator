package controller

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
	"github.com/example/redis-operator/internal/redis"
	"github.com/example/redis-operator/internal/resources"
)

// clusterReconciler returns a RedisClusterReconciler seeded with the given object.
// ClusterManager is intentionally left nil so tests exercise resource creation
// without needing a real Redis connection; the lifecycle path is guarded.
func clusterReconciler(t *testing.T, rc *redisv1alpha1.RedisCluster) *RedisClusterReconciler {
	t.Helper()
	s := buildScheme(t)
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&redisv1alpha1.RedisCluster{}).
		WithObjects(rc)
	return &RedisClusterReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(16),
		// ClusterManager: nil — lifecycle reconciliation is skipped in unit tests.
	}
}

func reconcileClusterOnce(t *testing.T, r *RedisClusterReconciler, name, ns string) ctrl.Result {
	t.Helper()
	g := NewWithT(t)
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: name, Namespace: ns},
	})
	g.Expect(err).NotTo(HaveOccurred())
	return res
}

// ---------- helpers ----------

func minimalCluster(name, ns string) *redisv1alpha1.RedisCluster {
	return &redisv1alpha1.RedisCluster{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: redisv1alpha1.RedisClusterSpec{
			Image:             "redis:7.2-alpine",
			Masters:           3,
			ReplicasPerMaster: 1,
		},
	}
}

func storageCluster(name, ns string) *redisv1alpha1.RedisCluster {
	className := "standard"
	rc := minimalCluster(name, ns)
	rc.Spec.Storage = &redisv1alpha1.StorageSpec{
		ClassName: &className,
		Size:      "10Gi",
	}
	return rc
}

// ---------- tests ----------

func TestClusterReconcile_AddsFinalizerOnFirstReconcile(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	r := clusterReconciler(t, rc)

	reconcileClusterOnce(t, r, "mycluster", "default")

	updated := &redisv1alpha1.RedisCluster{}
	g.Expect(r.Get(context.Background(),
		types.NamespacedName{Name: "mycluster", Namespace: "default"}, updated)).To(Succeed())
	g.Expect(updated.Finalizers).To(ContainElement(redisv1alpha1.FinalizerName))
}

func TestClusterReconcile_CreatesAllCoreResources(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	r := clusterReconciler(t, rc)

	// First reconcile: add finalizer + requeue.
	reconcileClusterOnce(t, r, "mycluster", "default")
	// Second reconcile: create resources.
	reconcileClusterOnce(t, r, "mycluster", "default")

	ctx := context.Background()

	// ConfigMap
	cm := &corev1.ConfigMap{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster-config", Namespace: "default"}, cm)).To(Succeed())

	// Master StatefulSet — BuildClusterMasterStatefulSet uses rc.Name+"-masters".
	masterSts := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster-masters", Namespace: "default"}, masterSts)).To(Succeed())
	g.Expect(masterSts.Spec.Template.Annotations).To(HaveKey(configHashAnnotation))

	// Replica StatefulSet — BuildClusterReplicaStatefulSet uses rc.Name+"-replicas".
	replicaSts := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster-replicas", Namespace: "default"}, replicaSts)).To(Succeed())

	// Headless Service — master
	masterHeadless := &corev1.Service{}
	masterHeadlessName := resources.HeadlessServiceName("mycluster", resources.ComponentMaster)
	g.Expect(r.Get(ctx, types.NamespacedName{Name: masterHeadlessName, Namespace: "default"}, masterHeadless)).To(Succeed())
	g.Expect(masterHeadless.Spec.ClusterIP).To(Equal("None"))

	// Headless Service — replica
	replicaHeadless := &corev1.Service{}
	replicaHeadlessName := resources.HeadlessServiceName("mycluster", resources.ComponentReplica)
	g.Expect(r.Get(ctx, types.NamespacedName{Name: replicaHeadlessName, Namespace: "default"}, replicaHeadless)).To(Succeed())
	g.Expect(replicaHeadless.Spec.ClusterIP).To(Equal("None"))

	// Client Service (same name as CR)
	clientSvc := &corev1.Service{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster", Namespace: "default"}, clientSvc)).To(Succeed())
	g.Expect(clientSvc.Spec.ClusterIP).NotTo(Equal("None"))

	// Master PDB
	masterPDB := &policyv1.PodDisruptionBudget{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster-masters-pdb", Namespace: "default"}, masterPDB)).To(Succeed())

	// Replica PDB
	replicaPDB := &policyv1.PodDisruptionBudget{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster-replicas-pdb", Namespace: "default"}, replicaPDB)).To(Succeed())
}

func TestClusterReconcile_ConfigHashInjectedInMasterStatefulSet(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	r := clusterReconciler(t, rc)

	reconcileClusterOnce(t, r, "mycluster", "default")
	reconcileClusterOnce(t, r, "mycluster", "default")

	sts := &appsv1.StatefulSet{}
	g.Expect(r.Get(context.Background(),
		types.NamespacedName{Name: "mycluster-masters", Namespace: "default"}, sts)).To(Succeed())

	hash := sts.Spec.Template.Annotations[configHashAnnotation]
	g.Expect(hash).To(HaveLen(16))
}

func TestClusterReconcile_ConfigUpdateChangesHash(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	r := clusterReconciler(t, rc)

	reconcileClusterOnce(t, r, "mycluster", "default")
	reconcileClusterOnce(t, r, "mycluster", "default")

	ctx := context.Background()

	sts := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster-masters", Namespace: "default"}, sts)).To(Succeed())
	hash1 := sts.Spec.Template.Annotations[configHashAnnotation]

	// Update spec with a new redis config entry.
	updated := &redisv1alpha1.RedisCluster{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster", Namespace: "default"}, updated)).To(Succeed())
	updated.Spec.RedisConfig = map[string]string{"maxmemory": "1gb"}
	g.Expect(r.Update(ctx, updated)).To(Succeed())

	reconcileClusterOnce(t, r, "mycluster", "default")

	sts2 := &appsv1.StatefulSet{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster-masters", Namespace: "default"}, sts2)).To(Succeed())
	hash2 := sts2.Spec.Template.Annotations[configHashAnnotation]

	g.Expect(hash2).NotTo(Equal(hash1), "config hash must change when redisConfig changes")
}

func TestClusterReconcile_Delete_RemovesFinalizer(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	rc.Finalizers = []string{redisv1alpha1.FinalizerName}
	now := metav1.Now()
	rc.DeletionTimestamp = &now

	r := clusterReconciler(t, rc)
	reconcileClusterOnce(t, r, "mycluster", "default")

	remaining := &redisv1alpha1.RedisCluster{}
	err := r.Get(context.Background(),
		types.NamespacedName{Name: "mycluster", Namespace: "default"}, remaining)
	if err == nil {
		g.Expect(remaining.Finalizers).NotTo(ContainElement(redisv1alpha1.FinalizerName))
	}
}

func TestClusterReconcile_Delete_WithStorage_DeletesPVCs(t *testing.T) {
	g := NewWithT(t)
	rc := storageCluster("mycluster", "default")
	rc.Finalizers = []string{redisv1alpha1.FinalizerName}
	now := metav1.Now()
	rc.DeletionTimestamp = &now

	// Seed a PVC that carries the cluster label.
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "data-mycluster-0",
			Namespace: "default",
			Labels:    map[string]string{"redis.example.com/cluster": "mycluster"},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("10Gi"),
				},
			},
		},
	}

	s := buildScheme(t)
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&redisv1alpha1.RedisCluster{}).
		WithObjects(rc, pvc)
	r := &RedisClusterReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(16),
	}

	reconcileClusterOnce(t, r, "mycluster", "default")

	remaining := &corev1.PersistentVolumeClaim{}
	err := r.Get(context.Background(),
		types.NamespacedName{Name: "data-mycluster-0", Namespace: "default"}, remaining)
	g.Expect(err).To(HaveOccurred(), "PVC should have been deleted")
}

func TestClusterReconcile_Delete_KeepAfterDeletion_PreservesPVCs(t *testing.T) {
	g := NewWithT(t)
	rc := storageCluster("mycluster", "default")
	rc.Spec.Storage.KeepAfterDeletion = true
	rc.Finalizers = []string{redisv1alpha1.FinalizerName}
	now := metav1.Now()
	rc.DeletionTimestamp = &now

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "data-mycluster-0",
			Namespace: "default",
			Labels:    map[string]string{"redis.example.com/cluster": "mycluster"},
		},
	}

	s := buildScheme(t)
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&redisv1alpha1.RedisCluster{}).
		WithObjects(rc, pvc)
	r := &RedisClusterReconciler{
		Client:   cb.Build(),
		Scheme:   s,
		Recorder: record.NewFakeRecorder(16),
	}

	reconcileClusterOnce(t, r, "mycluster", "default")

	remaining := &corev1.PersistentVolumeClaim{}
	err := r.Get(context.Background(),
		types.NamespacedName{Name: "data-mycluster-0", Namespace: "default"}, remaining)
	g.Expect(err).NotTo(HaveOccurred(), "PVC should be preserved when keepAfterDeletion=true")
}

func TestClusterReconcile_NilClusterManager_SkipsLifecycle(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	r := clusterReconciler(t, rc)
	// clusterReconciler sets ClusterManager to nil — lifecycle is skipped.

	reconcileClusterOnce(t, r, "mycluster", "default") // finalizer
	reconcileClusterOnce(t, r, "mycluster", "default") // resource creation

	// Resources are still created even without a ClusterManager.
	sts := &appsv1.StatefulSet{}
	g.Expect(r.Get(context.Background(),
		types.NamespacedName{Name: "mycluster-masters", Namespace: "default"}, sts)).To(Succeed())

	// Phase should be Initializing (not Ready) because lifecycle was skipped.
	updated := &redisv1alpha1.RedisCluster{}
	g.Expect(r.Get(context.Background(),
		types.NamespacedName{Name: "mycluster", Namespace: "default"}, updated)).To(Succeed())
	g.Expect(updated.Status.Phase).NotTo(Equal(redisv1alpha1.PhaseReady),
		"phase must not be Ready when cluster lifecycle is skipped")
}

// TestClusterRollingFailover_RecordsLastConfigHash verifies that tryClusterRollingFailover
// persists the current config hash in the CR annotation after the first reconcile.
func TestClusterRollingFailover_RecordsLastConfigHash(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	r := clusterReconciler(t, rc)
	// Wire up a noop ClusterManager so tryClusterRollingFailover runs.
	r.ClusterManager = &noopClusterManager{}
	r.RedisClient = &noopRedisClient{}

	reconcileClusterOnce(t, r, "mycluster", "default") // add finalizer
	reconcileClusterOnce(t, r, "mycluster", "default") // create resources + record hash

	updated := &redisv1alpha1.RedisCluster{}
	g.Expect(r.Get(context.Background(),
		types.NamespacedName{Name: "mycluster", Namespace: "default"}, updated)).To(Succeed())
	g.Expect(updated.Annotations).To(HaveKey(lastConfigHashAnnotation),
		"last-config-hash annotation should be recorded after reconcile")
	g.Expect(updated.Annotations[lastConfigHashAnnotation]).To(HaveLen(16))
}

// TestClusterRollingFailover_TriggersFailoverOnNonHotReloadableChange verifies that
// CLUSTER FAILOVER is called for each master's replica when the configHash changes
// and the new config contains a non-hot-reloadable parameter.
func TestClusterRollingFailover_TriggersFailoverOnNonHotReloadableChange(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	// Set an initial last-config-hash annotation so the change is detected.
	rc.Annotations = map[string]string{lastConfigHashAnnotation: "oldhashabcdefgh"}

	mockClient := &noopRedisClient{}
	// Return a cluster state with one master + one replica so we can verify failover call.
	clusterSt := &redis.ClusterState{
		State:         "ok",
		SlotsAssigned: 16384,
		Size:          3,
		Nodes: []redis.ClusterNode{
			{NodeID: "master1", Addr: "10.0.0.1:6379", Flags: []string{"master"}},
			{NodeID: "replica1", Addr: "10.0.0.2:6379", Flags: []string{"slave"}, MasterID: "master1"},
		},
	}

	s := buildScheme(t)
	cb := fake.NewClientBuilder().WithScheme(s).
		WithStatusSubresource(&redisv1alpha1.RedisCluster{}).
		WithObjects(rc)
	r := &RedisClusterReconciler{
		Client:         cb.Build(),
		Scheme:         s,
		Recorder:       record.NewFakeRecorder(32),
		RedisClient:    mockClient,
		ClusterManager: &noopClusterManager{state: clusterSt},
	}

	// Add a non-hot-reloadable config param so rolling failover is triggered.
	ctx := context.Background()
	updated := &redisv1alpha1.RedisCluster{}
	g.Expect(r.Get(ctx, types.NamespacedName{Name: "mycluster", Namespace: "default"}, updated)).To(Succeed())
	updated.Spec.RedisConfig = map[string]string{"appendonly": "yes"} // not in hotReloadableParams
	g.Expect(r.Update(ctx, updated)).To(Succeed())

	reconcileClusterOnce(t, r, "mycluster", "default") // finalizer
	reconcileClusterOnce(t, r, "mycluster", "default") // resources + rolling failover

	g.Expect(mockClient.clusterFailoverCalls).To(HaveLen(1),
		"CLUSTER FAILOVER should be triggered once (one master→replica pair)")
	g.Expect(mockClient.clusterFailoverCalls[0]).To(Equal("10.0.0.2:6379"),
		"CLUSTER FAILOVER should target the replica address")
}

func TestClusterReconcile_ReplicaStatefulSet_OnlyWhenReplicasPerMasterGtZero(t *testing.T) {
	g := NewWithT(t)
	rc := minimalCluster("mycluster", "default")
	rc.Spec.ReplicasPerMaster = 0 // no replicas
	r := clusterReconciler(t, rc)

	reconcileClusterOnce(t, r, "mycluster", "default")
	reconcileClusterOnce(t, r, "mycluster", "default")

	// Replica StatefulSet should NOT be created when replicasPerMaster=0.
	replicaSts := &appsv1.StatefulSet{}
	err := r.Get(context.Background(),
		types.NamespacedName{Name: "mycluster-replicas", Namespace: "default"}, replicaSts)
	g.Expect(err).To(HaveOccurred(), "replica StatefulSet must not exist when replicasPerMaster=0")
}
