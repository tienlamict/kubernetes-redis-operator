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

const sentinelMasterName = "mymaster"

// RedisSentinelReconciler reconciles a RedisSentinel object.
type RedisSentinelReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RedisClient redis.RedisClient
	Recorder    record.EventRecorder
}

// +kubebuilder:rbac:groups=redis.example.com,resources=redissentinels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.example.com,resources=redissentinels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redis.example.com,resources=redissentinels/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets;deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services;configmaps;secrets;persistentvolumeclaims;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the controller-runtime entry point. It records Prometheus metrics
// for every reconcile invocation and delegates to doReconcile for business logic.
func (r *RedisSentinelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	result, err := r.doReconcile(ctx, req)
	oprmetrics.RecordReconcile(oprmetrics.KindSentinel, start, err)
	return result, err
}

func (r *RedisSentinelReconciler) doReconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	rs := &redisv1alpha1.RedisSentinel{}
	if err := r.Get(ctx, req.NamespacedName, rs); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !rs.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, rs)
	}

	if !controllerutil.ContainsFinalizer(rs, redisv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(rs, redisv1alpha1.FinalizerName)
		if err := r.Update(ctx, rs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if rs.Status.Phase == "" {
		rs.Status.Phase = redisv1alpha1.PhaseInitializing
		if err := r.Status().Update(ctx, rs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// --- ConfigMaps ---
	redisConfigHash, err := r.reconcileRedisConfigMap(ctx, rs)
	if err != nil {
		logger.Error(err, "failed to reconcile Redis ConfigMap")
		r.setSentinelFailedStatus(ctx, rs, "ConfigMapFailed", err.Error())
		return ctrl.Result{}, err
	}

	masterFQDN := fmt.Sprintf("%s-0.%s.%s.svc.cluster.local",
		rs.Name, resources.HeadlessServiceName(rs.Name, resources.ComponentRedis), rs.Namespace)
	if err := r.reconcileSentinelConfigMap(ctx, rs, masterFQDN); err != nil {
		logger.Error(err, "failed to reconcile Sentinel ConfigMap")
		return ctrl.Result{}, err
	}

	// --- Workloads ---
	if err := r.reconcileRedisStatefulSet(ctx, rs, redisConfigHash); err != nil {
		logger.Error(err, "failed to reconcile Redis StatefulSet")
		return ctrl.Result{}, err
	}
	if err := r.reconcileSentinelDeployment(ctx, rs); err != nil {
		logger.Error(err, "failed to reconcile Sentinel Deployment")
		return ctrl.Result{}, err
	}

	// --- Services ---
	headless := resources.BuildHeadlessService(rs.Name, rs.Namespace, resources.ComponentRedis)
	if err := r.reconcileSentinelService(ctx, rs, headless); err != nil {
		logger.Error(err, "failed to reconcile headless Service")
		return ctrl.Result{}, err
	}
	for _, svc := range []client.Object{
		resources.BuildSentinelMasterService(rs),
		resources.BuildSentinelReplicaService(rs),
		resources.BuildSentinelService(rs),
	} {
		if err := r.reconcileSentinelService(ctx, rs, svc); err != nil {
			logger.Error(err, "failed to reconcile Service", "name", svc.GetName())
			return ctrl.Result{}, err
		}
	}

	// --- PodDisruptionBudgets ---
	for _, pdb := range []client.Object{
		resources.BuildSentinelRedisPDB(rs),
		resources.BuildSentinelPDB(rs),
	} {
		if err := r.reconcilePDB(ctx, rs, pdb); err != nil {
			logger.Error(err, "failed to reconcile PDB", "name", pdb.GetName())
			return ctrl.Result{}, err
		}
	}

	// --- ServiceMonitor (non-fatal) ---
	if rs.Spec.EnableExporter != nil && *rs.Spec.EnableExporter {
		if err := r.reconcileSentinelServiceMonitor(ctx, rs); err != nil {
			logger.V(1).Info("ServiceMonitor reconcile skipped", "reason", err.Error())
		}
	}

	// --- Master tracking (best-effort) ---
	r.reconcileRoleLabels(ctx, rs)

	// --- Status ---
	if err := r.updateSentinelStatus(ctx, rs); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// handleDeletion removes PVCs (unless keepAfterDeletion) and clears the finalizer.
func (r *RedisSentinelReconciler) handleDeletion(ctx context.Context, rs *redisv1alpha1.RedisSentinel) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(rs, redisv1alpha1.FinalizerName) {
		return ctrl.Result{}, nil
	}

	if rs.Spec.Storage != nil && !rs.Spec.Storage.KeepAfterDeletion {
		pvcList := &corev1.PersistentVolumeClaimList{}
		if err := r.List(ctx, pvcList,
			client.InNamespace(rs.Namespace),
			client.MatchingLabels(resources.PodLabels(rs.Name, resources.ComponentRedis)),
		); err == nil {
			for i := range pvcList.Items {
				if err := r.Delete(ctx, &pvcList.Items[i]); err != nil && !errors.IsNotFound(err) {
					return ctrl.Result{}, err
				}
			}
		}
	}

	r.Recorder.Event(rs, corev1.EventTypeNormal, "Deleted", "RedisSentinel instance cleaned up")
	controllerutil.RemoveFinalizer(rs, redisv1alpha1.FinalizerName)
	return ctrl.Result{}, r.Update(ctx, rs)
}

// reconcileRedisConfigMap creates/updates the Redis config ConfigMap; returns the data hash.
func (r *RedisSentinelReconciler) reconcileRedisConfigMap(ctx context.Context, rs *redisv1alpha1.RedisSentinel) (string, error) {
	desired := resources.BuildSentinelRedisConfigMap(rs)
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Data = desired.Data
		return ctrl.SetControllerReference(rs, existing, r.Scheme)
	})
	if err != nil {
		return "", err
	}
	return configDataHash(desired.Data), nil
}

// reconcileSentinelConfigMap creates/updates the sentinel.conf ConfigMap.
func (r *RedisSentinelReconciler) reconcileSentinelConfigMap(ctx context.Context, rs *redisv1alpha1.RedisSentinel, masterFQDN string) error {
	desired := resources.BuildSentinelConfigMap(rs, masterFQDN)
	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Data = desired.Data
		return ctrl.SetControllerReference(rs, existing, r.Scheme)
	})
	return err
}

// reconcileRedisStatefulSet creates/updates the Redis StatefulSet with the config hash annotation.
func (r *RedisSentinelReconciler) reconcileRedisStatefulSet(ctx context.Context, rs *redisv1alpha1.RedisSentinel, configHash string) error {
	desired := resources.BuildSentinelRedisStatefulSet(rs)
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
			existing.Spec.Template = desired.Spec.Template
			existing.Spec.Replicas = desired.Spec.Replicas
		}
		return ctrl.SetControllerReference(rs, existing, r.Scheme)
	})
	return err
}

// reconcileSentinelDeployment creates/updates the Sentinel Deployment.
func (r *RedisSentinelReconciler) reconcileSentinelDeployment(ctx context.Context, rs *redisv1alpha1.RedisSentinel) error {
	desired := resources.BuildSentinelDeployment(rs)
	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = desired.Spec
		} else {
			existing.Spec.Template = desired.Spec.Template
			existing.Spec.Replicas = desired.Spec.Replicas
		}
		return ctrl.SetControllerReference(rs, existing, r.Scheme)
	})
	return err
}

// reconcileSentinelService creates/updates a Service; preserves ClusterIP on update.
func (r *RedisSentinelReconciler) reconcileSentinelService(ctx context.Context, rs *redisv1alpha1.RedisSentinel, desired client.Object) error {
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
		return ctrl.SetControllerReference(rs, existing, r.Scheme)
	})
	return err
}

// reconcilePDB creates/updates a PodDisruptionBudget.
func (r *RedisSentinelReconciler) reconcilePDB(ctx context.Context, rs *redisv1alpha1.RedisSentinel, desired client.Object) error {
	pdb := desired.(*policyv1.PodDisruptionBudget)
	existing := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: pdb.Name, Namespace: pdb.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = pdb.Labels
		existing.Spec = pdb.Spec
		return ctrl.SetControllerReference(rs, existing, r.Scheme)
	})
	return err
}

// reconcileSentinelServiceMonitor creates/updates the Prometheus ServiceMonitor.
func (r *RedisSentinelReconciler) reconcileSentinelServiceMonitor(ctx context.Context, rs *redisv1alpha1.RedisSentinel) error {
	desired := resources.BuildSentinelServiceMonitor(rs)
	existing := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{Name: desired.Name, Namespace: desired.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		return ctrl.SetControllerReference(rs, existing, r.Scheme)
	})
	return err
}

// reconcileRoleLabels determines the current master pod and patches all Redis pods with
// their redis.example.com/role label (master or replica).  The function is best-effort:
// errors are logged at V(1) and never returned.
func (r *RedisSentinelReconciler) reconcileRoleLabels(ctx context.Context, rs *redisv1alpha1.RedisSentinel) {
	logger := log.FromContext(ctx)

	// Default: pod-0 is the initial master (before Sentinel has elected one).
	masterPodName := fmt.Sprintf("%s-0", rs.Name)

	// Try to discover the actual master from Sentinel.
	if r.RedisClient != nil {
		sentinelAddr := fmt.Sprintf("%s-sentinel.%s.svc.cluster.local:%d",
			rs.Name, rs.Namespace, resources.SentinelPort)
		masterHost, _, err := r.RedisClient.SentinelGetMasterAddr(ctx, sentinelAddr, sentinelMasterName)
		if err == nil && masterHost != "" {
			// Resolve the IP to a pod name.
			pods := &corev1.PodList{}
			if listErr := r.List(ctx, pods, client.InNamespace(rs.Namespace),
				client.MatchingLabels(resources.PodLabels(rs.Name, resources.ComponentRedis))); listErr == nil {
				for _, p := range pods.Items {
					if p.Status.PodIP == masterHost {
						masterPodName = p.Name
						break
					}
				}
			}
		}
	}

	// Patch each Redis pod with its role label.
	pods := &corev1.PodList{}
	if err := r.List(ctx, pods, client.InNamespace(rs.Namespace),
		client.MatchingLabels(resources.PodLabels(rs.Name, resources.ComponentRedis))); err != nil {
		logger.V(1).Info("role-label reconcile: cannot list pods", "err", err)
		return
	}

	for i := range pods.Items {
		pod := &pods.Items[i]
		desiredRole := resources.RoleReplica
		if pod.Name == masterPodName {
			desiredRole = resources.RoleMaster
		}
		if pod.Labels[resources.RoleLabelKey] == desiredRole {
			continue
		}
		patch := client.MergeFrom(pod.DeepCopy())
		if pod.Labels == nil {
			pod.Labels = make(map[string]string)
		}
		pod.Labels[resources.RoleLabelKey] = desiredRole
		if err := r.Patch(ctx, pod, patch); err != nil {
			logger.V(1).Info("role-label reconcile: cannot patch pod", "pod", pod.Name, "err", err)
		}
	}

	// Persist master pod name in status (caller will call r.Status().Update).
	rs.Status.MasterNode = masterPodName
}

// updateSentinelStatus counts ready pods and sets Phase + Conditions.
func (r *RedisSentinelReconciler) updateSentinelStatus(ctx context.Context, rs *redisv1alpha1.RedisSentinel) error {
	redisPods := &corev1.PodList{}
	if err := r.List(ctx, redisPods,
		client.InNamespace(rs.Namespace),
		client.MatchingLabels(resources.PodLabels(rs.Name, resources.ComponentRedis)),
	); err != nil {
		return err
	}

	sentinelPods := &corev1.PodList{}
	if err := r.List(ctx, sentinelPods,
		client.InNamespace(rs.Namespace),
		client.MatchingLabels(resources.PodLabels(rs.Name, resources.ComponentSentinel)),
	); err != nil {
		return err
	}

	readyRedis := countReadySentinelPods(redisPods)
	readySentinels := countReadySentinelPods(sentinelPods)

	allReady := int32(readyRedis) >= rs.Spec.Replicas && int32(readySentinels) >= rs.Spec.SentinelReplicas

	readyCond := metav1.Condition{
		Type:               conditionReady,
		Status:             boolConditionStatus(allReady),
		ObservedGeneration: rs.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reasonReady,
		Message: fmt.Sprintf("redis: %d/%d ready, sentinels: %d/%d ready",
			readyRedis, rs.Spec.Replicas, readySentinels, rs.Spec.SentinelReplicas),
	}
	if !allReady {
		readyCond.Reason = reasonReconciling
	}
	apimeta.SetStatusCondition(&rs.Status.Conditions, readyCond)

	if allReady {
		rs.Status.Phase = redisv1alpha1.PhaseReady
	} else if rs.Status.Phase != redisv1alpha1.PhaseFailed {
		rs.Status.Phase = redisv1alpha1.PhaseInitializing
	}
	rs.Status.ReadyReplicas = int32(readyRedis)
	rs.Status.ReadySentinels = int32(readySentinels)

	return r.Status().Update(ctx, rs)
}

func (r *RedisSentinelReconciler) setSentinelFailedStatus(ctx context.Context, rs *redisv1alpha1.RedisSentinel, reason, msg string) {
	apimeta.SetStatusCondition(&rs.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: rs.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	})
	rs.Status.Phase = redisv1alpha1.PhaseFailed
	_ = r.Status().Update(ctx, rs)
}

func countReadySentinelPods(list *corev1.PodList) int {
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
func (r *RedisSentinelReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1alpha1.RedisSentinel{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Complete(r)
}
