package controller

import (
	"context"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

// RedisReconciler reconciles a Redis object.
type RedisReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=redis.example.com,resources=redis,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.example.com,resources=redis/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redis.example.com,resources=redis/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets;deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services;configmaps;secrets;persistentvolumeclaims;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

func (r *RedisReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	redis := &redisv1alpha1.Redis{}
	if err := r.Get(ctx, req.NamespacedName, redis); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Handle deletion via finalizer.
	if !redis.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, redis)
	}

	// Ensure finalizer is present.
	if !controllerutil.ContainsFinalizer(redis, redisv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(redis, redisv1alpha1.FinalizerName)
		if err := r.Update(ctx, redis); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Set initial status.
	if redis.Status.Phase == "" {
		redis.Status.Phase = redisv1alpha1.PhaseInitializing
		if err := r.Status().Update(ctx, redis); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Reconcile ConfigMap.
	if err := r.reconcileConfigMap(ctx, redis); err != nil {
		logger.Error(err, "failed to reconcile ConfigMap")
		return ctrl.Result{}, err
	}

	// Reconcile StatefulSet or Deployment.
	if redis.Spec.Storage != nil {
		if err := r.reconcileStatefulSet(ctx, redis); err != nil {
			logger.Error(err, "failed to reconcile StatefulSet")
			return ctrl.Result{}, err
		}
	} else {
		if err := r.reconcileDeployment(ctx, redis); err != nil {
			logger.Error(err, "failed to reconcile Deployment")
			return ctrl.Result{}, err
		}
	}

	// Reconcile Service.
	if err := r.reconcileService(ctx, redis); err != nil {
		logger.Error(err, "failed to reconcile Service")
		return ctrl.Result{}, err
	}

	// Update status.
	if err := r.updateStatus(ctx, redis); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *RedisReconciler) handleDeletion(ctx context.Context, redis *redisv1alpha1.Redis) (ctrl.Result, error) {
	if controllerutil.ContainsFinalizer(redis, redisv1alpha1.FinalizerName) {
		// Clean up PVC if storage is configured and keepAfterDeletion is false.
		if redis.Spec.Storage != nil && !redis.Spec.Storage.KeepAfterDeletion {
			pvc := &corev1.PersistentVolumeClaim{}
			pvcName := client.ObjectKey{Name: "data-" + redis.Name + "-0", Namespace: redis.Namespace}
			if err := r.Get(ctx, pvcName, pvc); err == nil {
				_ = r.Delete(ctx, pvc)
			}
		}
		controllerutil.RemoveFinalizer(redis, redisv1alpha1.FinalizerName)
		return ctrl.Result{}, r.Update(ctx, redis)
	}
	return ctrl.Result{}, nil
}

func (r *RedisReconciler) reconcileConfigMap(ctx context.Context, redis *redisv1alpha1.Redis) error {
	desired := resources.BuildRedisConfigMap(redis)
	if err := ctrl.SetControllerReference(redis, desired, r.Scheme); err != nil {
		return err
	}
	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	existing.Data = desired.Data
	return r.Update(ctx, existing)
}

func (r *RedisReconciler) reconcileStatefulSet(ctx context.Context, redis *redisv1alpha1.Redis) error {
	desired := resources.BuildRedisStatefulSet(redis)
	if err := ctrl.SetControllerReference(redis, desired, r.Scheme); err != nil {
		return err
	}
	existing := &appsv1.StatefulSet{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	existing.Spec.Template = desired.Spec.Template
	return r.Update(ctx, existing)
}

func (r *RedisReconciler) reconcileDeployment(ctx context.Context, redis *redisv1alpha1.Redis) error {
	desired := resources.BuildRedisDeployment(redis)
	if err := ctrl.SetControllerReference(redis, desired, r.Scheme); err != nil {
		return err
	}
	existing := &appsv1.Deployment{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	if err != nil {
		return err
	}
	existing.Spec.Template = desired.Spec.Template
	return r.Update(ctx, existing)
}

func (r *RedisReconciler) reconcileService(ctx context.Context, redis *redisv1alpha1.Redis) error {
	desired := resources.BuildRedisService(redis)
	if err := ctrl.SetControllerReference(redis, desired, r.Scheme); err != nil {
		return err
	}
	existing := &corev1.Service{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if errors.IsNotFound(err) {
		return r.Create(ctx, desired)
	}
	return err
}

func (r *RedisReconciler) updateStatus(ctx context.Context, redis *redisv1alpha1.Redis) error {
	// Count ready pods.
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(redis.Namespace),
		client.MatchingLabels(resources.PodLabels(redis.Name, resources.ComponentRedis)),
	); err != nil {
		return err
	}

	readyCount := int32(0)
	for _, p := range podList.Items {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				readyCount++
			}
		}
	}

	phase := redisv1alpha1.PhaseInitializing
	if readyCount > 0 {
		phase = redisv1alpha1.PhaseReady
	}

	redis.Status.Phase = phase
	redis.Status.ReadyReplicas = readyCount
	redis.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             conditionStatus(readyCount > 0),
			ObservedGeneration: redis.Generation,
			LastTransitionTime: metav1.Now(),
			Reason:             phase,
			Message:            "",
		},
	}

	return r.Status().Update(ctx, redis)
}

func conditionStatus(ok bool) metav1.ConditionStatus {
	if ok {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

// SetupWithManager sets up the controller with the Manager.
func (r *RedisReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1alpha1.Redis{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
