package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
	oprmetrics "github.com/example/redis-operator/internal/metrics"
	"github.com/example/redis-operator/internal/redis"
	"github.com/example/redis-operator/internal/resources"
)

// RedisClusterReconciler reconciles a RedisCluster object.
type RedisClusterReconciler struct {
	client.Client
	Scheme         *runtime.Scheme
	RedisClient    redis.RedisClient
	ClusterManager redis.ClusterManager
	Recorder       record.EventRecorder
}

// +kubebuilder:rbac:groups=redis.example.com,resources=redisclusters,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.example.com,resources=redisclusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redis.example.com,resources=redisclusters/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services;configmaps;secrets;persistentvolumeclaims;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the controller-runtime entry point. It records Prometheus metrics
// for every reconcile invocation and delegates to doReconcile for business logic.
func (r *RedisClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	result, err := r.doReconcile(ctx, req)
	oprmetrics.RecordReconcile(oprmetrics.KindCluster, start, err)
	return result, err
}

func (r *RedisClusterReconciler) doReconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	rc := &redisv1alpha1.RedisCluster{}
	if err := r.Get(ctx, req.NamespacedName, rc); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !rc.DeletionTimestamp.IsZero() {
		return r.handleClusterDeletion(ctx, rc)
	}

	if !controllerutil.ContainsFinalizer(rc, redisv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(rc, redisv1alpha1.FinalizerName)
		if err := r.Update(ctx, rc); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if rc.Status.Phase == "" {
		rc.Status.Phase = redisv1alpha1.PhaseInitializing
		if err := r.Status().Update(ctx, rc); err != nil {
			return ctrl.Result{}, err
		}
	}

	// --- ConfigMap ---
	configHash, err := r.reconcileClusterConfigMap(ctx, rc)
	if err != nil {
		logger.Error(err, "failed to reconcile ConfigMap")
		r.setClusterFailedStatus(ctx, rc, "ConfigMapFailed", err.Error())
		return ctrl.Result{}, err
	}

	// --- StatefulSets ---
	if err := r.reconcileClusterMasterStatefulSet(ctx, rc, configHash); err != nil {
		logger.Error(err, "failed to reconcile master StatefulSet")
		return ctrl.Result{}, err
	}
	if rc.Spec.ReplicasPerMaster > 0 {
		if err := r.reconcileClusterReplicaStatefulSet(ctx, rc, configHash); err != nil {
			logger.Error(err, "failed to reconcile replica StatefulSet")
			return ctrl.Result{}, err
		}
	}

	// --- Services ---
	for _, svc := range []client.Object{
		resources.BuildHeadlessService(rc.Name, rc.Namespace, resources.ComponentMaster),
		resources.BuildHeadlessService(rc.Name, rc.Namespace, resources.ComponentReplica),
		resources.BuildClusterClientService(rc),
	} {
		if err := r.reconcileClusterService(ctx, rc, svc); err != nil {
			logger.Error(err, "failed to reconcile Service", "name", svc.GetName())
			return ctrl.Result{}, err
		}
	}

	// --- PodDisruptionBudgets ---
	for _, pdb := range []client.Object{
		resources.BuildClusterMasterPDB(rc),
		resources.BuildClusterReplicaPDB(rc),
	} {
		if err := r.reconcileClusterPDB(ctx, rc, pdb); err != nil {
			logger.Error(err, "failed to reconcile PDB", "name", pdb.GetName())
			return ctrl.Result{}, err
		}
	}

	// --- ServiceMonitor (non-fatal) ---
	if rc.Spec.EnableExporter != nil && *rc.Spec.EnableExporter {
		if err := r.reconcileClusterServiceMonitor(ctx, rc); err != nil {
			logger.V(1).Info("ServiceMonitor reconcile skipped", "reason", err.Error())
		}
	}

	// --- Cluster lifecycle management ---
	result, lifecycleErr := r.reconcileClusterLifecycle(ctx, rc)

	// --- Hot-reload (best-effort) ---
	r.tryClusterHotReload(ctx, rc)

	// --- Status ---
	if err := r.updateClusterStatus(ctx, rc); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	if lifecycleErr != nil || result.Requeue || result.RequeueAfter > 0 {
		return result, lifecycleErr
	}
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleClusterDeletion cleans up PVCs and removes the finalizer.
func (r *RedisClusterReconciler) handleClusterDeletion(ctx context.Context, rc *redisv1alpha1.RedisCluster) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(rc, redisv1alpha1.FinalizerName) {
		return ctrl.Result{}, nil
	}

	if rc.Spec.Storage != nil && !rc.Spec.Storage.KeepAfterDeletion {
		pvcList := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, pvcList,
			client.InNamespace(rc.Namespace),
			client.MatchingLabels(map[string]string{"redis.example.com/cluster": rc.Name}),
		); err == nil {
			for i := range pvcList.Items {
				if err := r.Delete(ctx, &pvcList.Items[i]); err != nil && !errors.IsNotFound(err) {
					return ctrl.Result{}, err
				}
			}
		}
	}

	// Remove per-cluster Prometheus gauge series so stale metrics are not exported.
	oprmetrics.DeleteClusterMetrics(rc.Name, rc.Namespace)
	r.Recorder.Event(rc, corev1.EventTypeNormal, "Deleted", "RedisCluster instance cleaned up")
	controllerutil.RemoveFinalizer(rc, redisv1alpha1.FinalizerName)
	return ctrl.Result{}, r.Update(ctx, rc)
}

// reconcileClusterConfigMap creates/updates the cluster ConfigMap and returns the data hash.
func (r *RedisClusterReconciler) reconcileClusterConfigMap(ctx context.Context, rc *redisv1alpha1.RedisCluster) (string, error) {
	desired := resources.BuildClusterConfigMap(rc)
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Data = desired.Data
		return ctrl.SetControllerReference(rc, existing, r.Scheme)
	})
	if err != nil {
		return "", err
	}
	return configDataHash(desired.Data), nil
}

// reconcileClusterMasterStatefulSet creates/updates the master StatefulSet.
func (r *RedisClusterReconciler) reconcileClusterMasterStatefulSet(ctx context.Context, rc *redisv1alpha1.RedisCluster, configHash string) error {
	desired := resources.BuildClusterMasterStatefulSet(rc)
	if desired.Spec.Template.Annotations == nil {
		desired.Spec.Template.Annotations = make(map[string]string)
	}
	desired.Spec.Template.Annotations[configHashAnnotation] = configHash

	existing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = desired.Spec
		} else {
			existing.Spec.Replicas = desired.Spec.Replicas
			existing.Spec.Template = desired.Spec.Template
		}
		return ctrl.SetControllerReference(rc, existing, r.Scheme)
	})
	return err
}

// reconcileClusterReplicaStatefulSet creates/updates the replica StatefulSet.
func (r *RedisClusterReconciler) reconcileClusterReplicaStatefulSet(ctx context.Context, rc *redisv1alpha1.RedisCluster, configHash string) error {
	desired := resources.BuildClusterReplicaStatefulSet(rc)
	if desired.Spec.Template.Annotations == nil {
		desired.Spec.Template.Annotations = make(map[string]string)
	}
	desired.Spec.Template.Annotations[configHashAnnotation] = configHash

	existing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = desired.Spec
		} else {
			existing.Spec.Replicas = desired.Spec.Replicas
			existing.Spec.Template = desired.Spec.Template
		}
		return ctrl.SetControllerReference(rc, existing, r.Scheme)
	})
	return err
}

// reconcileClusterService creates/updates a Service, preserving ClusterIP.
func (r *RedisClusterReconciler) reconcileClusterService(ctx context.Context, rc *redisv1alpha1.RedisCluster, desired client.Object) error {
	svc := desired.(*corev1.Service)
	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: svc.Name, Namespace: svc.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = svc.Labels
		existing.Annotations = svc.Annotations
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = svc.Spec
		} else {
			existing.Spec.Selector = svc.Spec.Selector
			existing.Spec.Ports = svc.Spec.Ports
			existing.Spec.PublishNotReadyAddresses = svc.Spec.PublishNotReadyAddresses
		}
		return ctrl.SetControllerReference(rc, existing, r.Scheme)
	})
	return err
}

// reconcileClusterPDB creates/updates a PodDisruptionBudget.
func (r *RedisClusterReconciler) reconcileClusterPDB(ctx context.Context, rc *redisv1alpha1.RedisCluster, desired client.Object) error {
	pdb := desired.(*policyv1.PodDisruptionBudget)
	existing := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: pdb.Name, Namespace: pdb.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = pdb.Labels
		existing.Spec = pdb.Spec
		return ctrl.SetControllerReference(rc, existing, r.Scheme)
	})
	return err
}

// reconcileClusterServiceMonitor creates/updates the Prometheus ServiceMonitor.
func (r *RedisClusterReconciler) reconcileClusterServiceMonitor(ctx context.Context, rc *redisv1alpha1.RedisCluster) error {
	desired := resources.BuildClusterServiceMonitor(rc)
	existing := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		return ctrl.SetControllerReference(rc, existing, r.Scheme)
	})
	return err
}

// =============================================================================
// Cluster Lifecycle Management (formation, scaling, failover)
// =============================================================================

// reconcileClusterLifecycle drives the Redis Cluster state machine.
// It returns a result/error only when a non-standard requeue interval is needed.
func (r *RedisClusterReconciler) reconcileClusterLifecycle(ctx context.Context, rc *redisv1alpha1.RedisCluster) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if r.ClusterManager == nil {
		// No ClusterManager (unit tests without real Redis); skip lifecycle.
		return ctrl.Result{}, nil
	}

	// Probe the cluster from any master node.
	probeAddr := clusterMasterAddr(rc, 0)
	clusterState, err := r.ClusterManager.GetClusterState(ctx, probeAddr)
	if err != nil {
		// Cannot reach Redis — pods may still be starting up.
		if !r.allMasterPodsReady(ctx, rc) {
			rc.Status.Phase = redisv1alpha1.PhaseInitializing
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		logger.V(1).Info("cannot reach cluster, will retry", "err", err)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// ── Cluster not yet initialised ──────────────────────────────────────────
	if clusterState.SlotsAssigned == 0 {
		if !r.allPodsReady(ctx, rc) {
			rc.Status.Phase = redisv1alpha1.PhaseInitializing
			return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
		}
		logger.Info("forming Redis Cluster")
		masterAddrs := allMasterAddrs(rc)
		replicaAddrs := allReplicaAddrs(rc)
		if err := r.ClusterManager.FormCluster(ctx, masterAddrs, replicaAddrs, int(rc.Spec.ReplicasPerMaster)); err != nil {
			r.setClusterFailedStatus(ctx, rc, "ClusterFormationFailed", err.Error())
			r.Recorder.Event(rc, corev1.EventTypeWarning, "ClusterFormationFailed", err.Error())
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		r.Recorder.Event(rc, corev1.EventTypeNormal, "ClusterFormed", "Redis Cluster formed successfully")
		rc.Status.Phase = redisv1alpha1.PhaseReady
		return ctrl.Result{Requeue: true}, nil
	}

	// ── Cluster is in fail state ─────────────────────────────────────────────
	if clusterState.State == "fail" {
		if rc.Status.Phase != redisv1alpha1.PhaseRecovering {
			rc.Status.Phase = redisv1alpha1.PhaseRecovering
			oprmetrics.RecordClusterFailover(rc.Name, rc.Namespace)
			r.Recorder.Event(rc, corev1.EventTypeWarning, "ClusterFailing",
				"Redis Cluster is in fail state; waiting for automatic recovery")
		}
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	// Cluster recovered from fail state.
	if rc.Status.Phase == redisv1alpha1.PhaseRecovering && clusterState.State == "ok" {
		rc.Status.Phase = redisv1alpha1.PhaseReady
		r.Recorder.Event(rc, corev1.EventTypeNormal, "ClusterRecovered", "Redis Cluster has recovered")
	}

	// ── Scale detection ──────────────────────────────────────────────────────
	currentMasters := int32(clusterState.Size)
	if currentMasters > 0 && currentMasters != rc.Spec.Masters {
		if rc.Spec.Masters > currentMasters {
			return r.reconcileScaleOut(ctx, rc, currentMasters)
		}
		return r.reconcileScaleIn(ctx, rc, currentMasters)
	}

	return ctrl.Result{}, nil
}

// reconcileScaleOut adds new master (and replica) nodes and rebalances slots.
func (r *RedisClusterReconciler) reconcileScaleOut(ctx context.Context, rc *redisv1alpha1.RedisCluster, currentMasters int32) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	scalingStart := time.Now()
	rc.Status.Phase = redisv1alpha1.PhaseScaling

	if !r.allMasterPodsReady(ctx, rc) {
		logger.Info("waiting for new master pods to become ready before scale-out")
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	existingAddr := clusterMasterAddr(rc, 0)

	// Meet each new master with the existing cluster.
	for i := currentMasters; i < rc.Spec.Masters; i++ {
		newAddr := clusterMasterAddr(rc, i)
		if err := r.ClusterManager.AddMaster(ctx, existingAddr, newAddr); err != nil {
			logger.Error(err, "CLUSTER MEET failed for new master", "addr", newAddr)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	// Rebalance slots: pass all desired masters so new ones receive slots.
	if err := r.ClusterManager.RebalanceSlots(ctx, allMasterAddrs(rc)); err != nil {
		logger.Error(err, "slot rebalance failed during scale-out")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Add replicas for the new masters if configured.
	if rc.Spec.ReplicasPerMaster > 0 {
		for i := currentMasters; i < rc.Spec.Masters; i++ {
			masterAddr := clusterMasterAddr(rc, i)
			for j := int32(0); j < rc.Spec.ReplicasPerMaster; j++ {
				replicaIdx := i*rc.Spec.ReplicasPerMaster + j
				replicaAddr := clusterReplicaAddr(rc, replicaIdx)
				if err := r.ClusterManager.AddReplica(ctx, replicaAddr, masterAddr); err != nil {
					logger.Error(err, "failed to attach replica", "replica", replicaAddr, "master", masterAddr)
				}
			}
		}
	}

	oprmetrics.RecordClusterScaling(rc.Name, rc.Namespace, "out", scalingStart)
	r.Recorder.Event(rc, corev1.EventTypeNormal, "ScaleOut",
		fmt.Sprintf("Scaled from %d to %d masters", currentMasters, rc.Spec.Masters))
	rc.Status.Phase = redisv1alpha1.PhaseReady
	return ctrl.Result{Requeue: true}, nil
}

// reconcileScaleIn migrates slots away from excess masters, then removes them.
func (r *RedisClusterReconciler) reconcileScaleIn(ctx context.Context, rc *redisv1alpha1.RedisCluster, currentMasters int32) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	scalingStart := time.Now()
	rc.Status.Phase = redisv1alpha1.PhaseScaling

	existingAddr := clusterMasterAddr(rc, 0)
	remainingAddrs := make([]string, rc.Spec.Masters)
	for i := int32(0); i < rc.Spec.Masters; i++ {
		remainingAddrs[i] = clusterMasterAddr(rc, i)
	}

	// Rebalance slots so excess masters are drained.
	if err := r.ClusterManager.RebalanceSlots(ctx, remainingAddrs); err != nil {
		logger.Error(err, "slot rebalance failed during scale-in")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Remove excess masters (highest indices first).
	for i := currentMasters - 1; i >= rc.Spec.Masters; i-- {
		// Remove associated replicas first.
		if rc.Spec.ReplicasPerMaster > 0 {
			for j := int32(0); j < rc.Spec.ReplicasPerMaster; j++ {
				replicaIdx := i*rc.Spec.ReplicasPerMaster + j
				replicaAddr := clusterReplicaAddr(rc, replicaIdx)
				if err := r.ClusterManager.RemoveReplica(ctx, existingAddr, replicaAddr); err != nil {
					logger.V(1).Info("RemoveReplica non-fatal", "addr", replicaAddr, "err", err)
				}
			}
		}
		removeAddr := clusterMasterAddr(rc, i)
		if err := r.ClusterManager.RemoveMaster(ctx, existingAddr, removeAddr); err != nil {
			logger.Error(err, "RemoveMaster failed", "addr", removeAddr)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	oprmetrics.RecordClusterScaling(rc.Name, rc.Namespace, "in", scalingStart)
	r.Recorder.Event(rc, corev1.EventTypeNormal, "ScaleIn",
		fmt.Sprintf("Scaled from %d to %d masters", currentMasters, rc.Spec.Masters))
	rc.Status.Phase = redisv1alpha1.PhaseReady
	return ctrl.Result{Requeue: true}, nil
}

// tryClusterHotReload applies hot-reloadable config params via CONFIG SET on every cluster node.
func (r *RedisClusterReconciler) tryClusterHotReload(ctx context.Context, rc *redisv1alpha1.RedisCluster) {
	if r.RedisClient == nil || rc.Spec.RedisConfig == nil {
		return
	}
	logger := log.FromContext(ctx)
	for i := int32(0); i < rc.Spec.Masters; i++ {
		addr := clusterMasterAddr(rc, i)
		for k, v := range rc.Spec.RedisConfig {
			if !hotReloadableParams[k] {
				continue
			}
			if err := r.RedisClient.ConfigSet(ctx, addr, k, v); err != nil {
				logger.V(1).Info("hot-reload CONFIG SET failed", "addr", addr, "key", k, "err", err)
			}
		}
	}
}

// updateClusterStatus synchronises the cluster status subresource.
func (r *RedisClusterReconciler) updateClusterStatus(ctx context.Context, rc *redisv1alpha1.RedisCluster) error {
	masterPods := &corev1.PodList{}
	_ = r.List(ctx, masterPods,
		client.InNamespace(rc.Namespace),
		client.MatchingLabels(resources.PodLabels(rc.Name, resources.ComponentMaster)))

	replicaPods := &corev1.PodList{}
	_ = r.List(ctx, replicaPods,
		client.InNamespace(rc.Namespace),
		client.MatchingLabels(resources.PodLabels(rc.Name, resources.ComponentReplica)))

	readyMasters := int32(countReadyPods(masterPods))
	readyReplicas := int32(countReadyPods(replicaPods))

	// Try to enrich from CLUSTER NODES.
	clusterState := "unknown"
	assignedSlots := int32(0)
	var nodeStatuses []redisv1alpha1.RedisNodeStatus

	if r.ClusterManager != nil {
		if state, err := r.ClusterManager.GetClusterState(ctx, clusterMasterAddr(rc, 0)); err == nil {
			clusterState = state.State
			assignedSlots = int32(state.SlotsAssigned)
			for _, n := range state.Nodes {
				role := "replica"
				if len(n.Flags) > 0 && n.Flags[0] == "master" {
					role = "master"
				}
				slotStr := ""
				if len(n.Slots) > 0 {
					slotStr = fmt.Sprintf("%d-%d", n.Slots[0].Start, n.Slots[len(n.Slots)-1].End)
				}
				nodeStatuses = append(nodeStatuses, redisv1alpha1.RedisNodeStatus{
					NodeID:  n.NodeID,
					IP:      n.Addr,
					Role:    role,
					Slots:   slotStr,
					Health:  "ok",
				})
			}
		}
	}

	allReady := readyMasters >= rc.Spec.Masters
	totalExpectedReplicas := rc.Spec.Masters * rc.Spec.ReplicasPerMaster
	if rc.Spec.ReplicasPerMaster > 0 {
		allReady = allReady && readyReplicas >= totalExpectedReplicas
	}

	readyCond := metav1.Condition{
		Type:               conditionReady,
		Status:             boolConditionStatus(allReady),
		ObservedGeneration: rc.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reasonReady,
		Message: fmt.Sprintf("masters: %d/%d ready, replicas: %d/%d ready, slots: %d/16384, state: %s",
			readyMasters, rc.Spec.Masters, readyReplicas, totalExpectedReplicas, assignedSlots, clusterState),
	}
	if !allReady {
		readyCond.Reason = reasonReconciling
	}
	apimeta.SetStatusCondition(&rc.Status.Conditions, readyCond)

	if rc.Status.Phase == "" || rc.Status.Phase == redisv1alpha1.PhaseInitializing && allReady {
		if allReady && clusterState == "ok" {
			rc.Status.Phase = redisv1alpha1.PhaseReady
		} else {
			rc.Status.Phase = redisv1alpha1.PhaseInitializing
		}
	}

	rc.Status.ReadyMasters = readyMasters
	rc.Status.ReadyReplicas = readyReplicas
	rc.Status.ClusterState = clusterState
	rc.Status.AssignedSlots = assignedSlots
	rc.Status.Nodes = nodeStatuses

	// Update Prometheus gauges with the freshly computed values.
	oprmetrics.SetClusterMetrics(rc.Name, rc.Namespace, clusterState,
		assignedSlots, int32(len(nodeStatuses)), readyMasters, readyReplicas)

	return r.Status().Update(ctx, rc)
}

func (r *RedisClusterReconciler) setClusterFailedStatus(ctx context.Context, rc *redisv1alpha1.RedisCluster, reason, msg string) {
	apimeta.SetStatusCondition(&rc.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: rc.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	})
	rc.Status.Phase = redisv1alpha1.PhaseFailed
	_ = r.Status().Update(ctx, rc)
}

// allMasterPodsReady returns true when at least spec.Masters pods are Ready.
func (r *RedisClusterReconciler) allMasterPodsReady(ctx context.Context, rc *redisv1alpha1.RedisCluster) bool {
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(rc.Namespace),
		client.MatchingLabels(resources.PodLabels(rc.Name, resources.ComponentMaster))); err != nil {
		return false
	}
	return int32(countReadyPods(pods)) >= rc.Spec.Masters
}

// allPodsReady returns true when all expected master and replica pods are Ready.
func (r *RedisClusterReconciler) allPodsReady(ctx context.Context, rc *redisv1alpha1.RedisCluster) bool {
	if !r.allMasterPodsReady(ctx, rc) {
		return false
	}
	if rc.Spec.ReplicasPerMaster == 0 {
		return true
	}
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods,
		client.InNamespace(rc.Namespace),
		client.MatchingLabels(resources.PodLabels(rc.Name, resources.ComponentReplica))); err != nil {
		return false
	}
	return int32(countReadyPods(pods)) >= rc.Spec.Masters*rc.Spec.ReplicasPerMaster
}

// =============================================================================
// Address helpers — build headless-DNS addresses for StatefulSet pods
// =============================================================================

func clusterMasterAddr(rc *redisv1alpha1.RedisCluster, idx int32) string {
	stsName := resources.ClusterMasterStatefulSetName(rc.Name)
	headless := resources.HeadlessServiceName(rc.Name, resources.ComponentMaster)
	return fmt.Sprintf("%s-%d.%s.%s.svc.cluster.local:%d",
		stsName, idx, headless, rc.Namespace, resources.RedisPort)
}

func clusterReplicaAddr(rc *redisv1alpha1.RedisCluster, idx int32) string {
	stsName := resources.ClusterReplicaStatefulSetName(rc.Name)
	headless := resources.HeadlessServiceName(rc.Name, resources.ComponentReplica)
	return fmt.Sprintf("%s-%d.%s.%s.svc.cluster.local:%d",
		stsName, idx, headless, rc.Namespace, resources.RedisPort)
}

func allMasterAddrs(rc *redisv1alpha1.RedisCluster) []string {
	addrs := make([]string, rc.Spec.Masters)
	for i := int32(0); i < rc.Spec.Masters; i++ {
		addrs[i] = clusterMasterAddr(rc, i)
	}
	return addrs
}

func allReplicaAddrs(rc *redisv1alpha1.RedisCluster) []string {
	total := rc.Spec.Masters * rc.Spec.ReplicasPerMaster
	addrs := make([]string, total)
	for i := int32(0); i < total; i++ {
		addrs[i] = clusterReplicaAddr(rc, i)
	}
	return addrs
}

// countReadyPods counts how many pods in the list have the PodReady condition true.
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

// SetupWithManager registers the controller with the Manager.
func (r *RedisClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1alpha1.RedisCluster{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Complete(r)
}
