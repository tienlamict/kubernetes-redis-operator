package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
	"github.com/example/redis-operator/internal/resources"
)

// RedisSentinelReconciler reconciles a RedisSentinel object.
type RedisSentinelReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=redis.example.com,resources=redissentinels,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.example.com,resources=redissentinels/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redis.example.com,resources=redissentinels/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets;deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services;configmaps;secrets;persistentvolumeclaims;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

func (r *RedisSentinelReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
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
	}

	if rs.Status.Phase == "" {
		rs.Status.Phase = redisv1alpha1.PhaseInitializing
		if err := r.Status().Update(ctx, rs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile Redis ConfigMap.
	if err := r.reconcileObject(ctx, rs, resources.BuildSentinelRedisConfigMap(rs)); err != nil {
		logger.Error(err, "failed to reconcile Redis ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile Sentinel ConfigMap (use first pod as placeholder master — init container handles real setup).
	masterIP := rs.Name + "-0." + resources.HeadlessServiceName(rs.Name, resources.ComponentRedis) +
		"." + rs.Namespace + ".svc.cluster.local"
	if err := r.reconcileObject(ctx, rs, resources.BuildSentinelConfigMap(rs, masterIP)); err != nil {
		logger.Error(err, "failed to reconcile Sentinel ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile Redis StatefulSet.
	if err := r.reconcileObject(ctx, rs, resources.BuildSentinelRedisStatefulSet(rs)); err != nil {
		logger.Error(err, "failed to reconcile Redis StatefulSet")
		return ctrl.Result{}, err
	}

	// Reconcile Sentinel Deployment.
	if err := r.reconcileObject(ctx, rs, resources.BuildSentinelDeployment(rs)); err != nil {
		logger.Error(err, "failed to reconcile Sentinel Deployment")
		return ctrl.Result{}, err
	}

	// Reconcile Services.
	for _, svc := range []client.Object{
		resources.BuildHeadlessService(rs.Name, rs.Namespace, resources.ComponentRedis),
		resources.BuildSentinelMasterService(rs),
		resources.BuildSentinelReplicaService(rs),
		resources.BuildSentinelService(rs),
	} {
		if err := r.reconcileObject(ctx, rs, svc); err != nil {
			logger.Error(err, "failed to reconcile Service", "name", svc.GetName())
			return ctrl.Result{}, err
		}
	}

	// Reconcile PodDisruptionBudgets.
	for _, pdb := range []client.Object{
		resources.BuildSentinelRedisPDB(rs),
		resources.BuildSentinelPDB(rs),
	} {
		if err := r.reconcileObject(ctx, rs, pdb); err != nil {
			logger.Error(err, "failed to reconcile PDB", "name", pdb.GetName())
			return ctrl.Result{}, err
		}
	}

	if err := r.updateStatus(ctx, rs); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *RedisSentinelReconciler) reconcileObject(ctx context.Context, owner metav1.Object, obj client.Object) error {
	if err := ctrl.SetControllerReference(owner, obj, r.Scheme); err != nil {
		return err
	}
	existing := obj.DeepCopyObject().(client.Object)
	err := r.Get(ctx, client.ObjectKeyFromObject(obj), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, obj)
	}
	return err
}

func (r *RedisSentinelReconciler) handleDeletion(ctx context.Context, rs *redisv1alpha1.RedisSentinel) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(rs, redisv1alpha1.FinalizerName) {
		controllerutil.RemoveFinalizer(rs, redisv1alpha1.FinalizerName)
		return ctrl.Result{}, r.Update(ctx, rs)
	}
	return ctrl.Result{}, nil
}

func (r *RedisSentinelReconciler) updateStatus(ctx context.Context, rs *redisv1alpha1.RedisSentinel) error {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(rs.Namespace),
		client.MatchingLabels(resources.PodLabels(rs.Name, resources.ComponentRedis)),
	); err != nil {
		return err
	}

	readyRedis := int32(0)
	for _, p := range podList.Items {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				readyRedis++
			}
		}
	}

	sentinelList := &corev1.PodList{}
	if err := r.List(ctx, sentinelList,
		client.InNamespace(rs.Namespace),
		client.MatchingLabels(resources.PodLabels(rs.Name, resources.ComponentSentinel)),
	); err != nil {
		return err
	}

	readySentinels := int32(0)
	for _, p := range sentinelList.Items {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				readySentinels++
			}
		}
	}

	phase := redisv1alpha1.PhaseInitializing
	if readyRedis >= rs.Spec.Replicas && readySentinels >= rs.Spec.SentinelReplicas {
		phase = redisv1alpha1.PhaseReady
	}

	rs.Status.Phase = phase
	rs.Status.ReadyReplicas = readyRedis
	rs.Status.ReadySentinels = readySentinels
	return r.Status().Update(ctx, rs)
}

// SetupWithManager sets up the controller with the Manager.
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
