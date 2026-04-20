package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
	"github.com/example/redis-operator/internal/redis"
	"github.com/example/redis-operator/internal/resources"
)

// RedisClusterReconciler reconciles a RedisCluster object.
type RedisClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	RedisClient    redis.RedisClient
	ClusterManager redis.ClusterManager
}

// +kubebuilder:rbac:groups=redis.example.com,resources=redisclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.example.com,resources=redisclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redis.example.com,resources=redisclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services;configmaps;secrets;persistentvolumeclaims;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *RedisClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	rc := &redisv1alpha1.RedisCluster{}
	if err := r.Get(ctx, req.NamespacedName, rc); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !rc.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, rc)
	}

	if !controllerutil.ContainsFinalizer(rc, redisv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(rc, redisv1alpha1.FinalizerName)
		if err := r.Update(ctx, rc); err != nil {
			return ctrl.Result{}, err
		}
	}

	if rc.Status.Phase == "" {
		rc.Status.Phase = redisv1alpha1.PhaseInitializing
		if err := r.Status().Update(ctx, rc); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile ConfigMap.
	if err := r.reconcileClusterObject(ctx, rc, resources.BuildClusterConfigMap(rc)); err != nil {
		logger.Error(err, "failed to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile master StatefulSet.
	if err := r.reconcileClusterObject(ctx, rc, resources.BuildClusterMasterStatefulSet(rc)); err != nil {
		logger.Error(err, "failed to reconcile master StatefulSet")
		return ctrl.Result{}, err
	}

	// Reconcile replica StatefulSet (only if replicasPerMaster > 0).
	if rc.Spec.ReplicasPerMaster > 0 {
		if err := r.reconcileClusterObject(ctx, rc, resources.BuildClusterReplicaStatefulSet(rc)); err != nil {
			logger.Error(err, "failed to reconcile replica StatefulSet")
			return ctrl.Result{}, err
		}
	}

	// Reconcile Services.
	for _, svc := range []client.Object{
		resources.BuildHeadlessService(rc.Name, rc.Namespace, resources.ComponentMaster),
		resources.BuildHeadlessService(rc.Name, rc.Namespace, resources.ComponentReplica),
		resources.BuildClusterClientService(rc),
	} {
		if err := r.reconcileClusterObject(ctx, rc, svc); err != nil {
			logger.Error(err, "failed to reconcile Service", "name", svc.GetName())
			return ctrl.Result{}, err
		}
	}

	// Reconcile PodDisruptionBudgets.
	for _, pdb := range []client.Object{
		resources.BuildClusterMasterPDB(rc),
		resources.BuildClusterReplicaPDB(rc),
	} {
		if err := r.reconcileClusterObject(ctx, rc, pdb); err != nil {
			logger.Error(err, "failed to reconcile PDB", "name", pdb.GetName())
			return ctrl.Result{}, err
		}
	}

	// Cluster formation and scaling logic is implemented in Phase 1D.
	// For now, update status based on pod readiness.
	if err := r.updateClusterStatus(ctx, rc); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *RedisClusterReconciler) reconcileClusterObject(ctx context.Context, rc *redisv1alpha1.RedisCluster, obj client.Object) error {
	if err := ctrl.SetControllerReference(rc, obj, r.Scheme); err != nil {
		return err
	}
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	return err
}

func (r *RedisClusterReconciler) handleDeletion(ctx context.Context, rc *redisv1alpha1.RedisCluster) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(rc, redisv1alpha1.FinalizerName) {
		// Delete PVCs if keepAfterDeletion is false.
		if rc.Spec.Storage != nil && !rc.Spec.Storage.KeepAfterDeletion {
			pvcList := &corev1.PersistentVolumeClaimList{}
			if err := r.List(ctx, pvcList,
				client.InNamespace(rc.Namespace),
				client.MatchingLabels(map[string]string{"redis.example.com/cluster": rc.Name}),
			); err == nil {
				for i := range pvcList.Items {
					_ = r.Delete(ctx, &pvcList.Items[i])
				}
			}
		}
		controllerutil.RemoveFinalizer(rc, redisv1alpha1.FinalizerName)
		return ctrl.Result{}, r.Update(ctx, rc)
	}
	return ctrl.Result{}, nil
}

func (r *RedisClusterReconciler) updateClusterStatus(ctx context.Context, rc *redisv1alpha1.RedisCluster) error {
	masterPods := &corev1.PodList{}
	if err := r.List(ctx, masterPods,
		client.InNamespace(rc.Namespace),
		client.MatchingLabels(resources.PodLabels(rc.Name, resources.ComponentMaster)),
	); err != nil {
		return err
	}

	replicaPods := &corev1.PodList{}
	if err := r.List(ctx, replicaPods,
		client.InNamespace(rc.Namespace),
		client.MatchingLabels(resources.PodLabels(rc.Name, resources.ComponentReplica)),
	); err != nil {
		return err
	}

	readyMasters := countReadyPods(masterPods)
	readyReplicas := countReadyPods(replicaPods)

	phase := rc.Status.Phase
	if phase == redisv1alpha1.PhaseInitializing {
		totalExpected := rc.Spec.Masters + rc.Spec.Masters*rc.Spec.ReplicasPerMaster
		totalReady := readyMasters + readyReplicas
		if int32(totalReady) >= totalExpected {
			phase = redisv1alpha1.PhaseReady
		}
	}

	rc.Status.Phase = phase
	rc.Status.ReadyMasters = int32(readyMasters)
	rc.Status.ReadyReplicas = int32(readyReplicas)

	return r.Status().Update(ctx, rc)
}

func countReadyPods(list *corev1.PodList) int {
	count := 0
	for _, p := range list.Items {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				count++
			}
		}
	}
	return count
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedisClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1alpha1.RedisCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Complete(r)
}
