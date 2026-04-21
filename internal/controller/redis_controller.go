package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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

const (
	configHashAnnotation = "redis.example.com/config-hash"
	conditionReady       = "Ready"

	reasonReady       = "Ready"
	reasonReconciling = "Reconciling"
)

// hotReloadableParams are Redis config keys that can be changed at runtime via CONFIG SET.
var hotReloadableParams = map[string]bool{
	"maxmemory":               true,
	"maxmemory-policy":        true,
	"hz":                      true,
	"loglevel":                true,
	"save":                    true,
	"timeout":                 true,
	"tcp-keepalive":           true,
	"slowlog-log-slower-than": true,
	"slowlog-max-len":         true,
}

// RedisReconciler reconciles a Redis object.
type RedisReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	RedisClient redis.RedisClient
	Recorder    record.EventRecorder
}

// +kubebuilder:rbac:groups=redis.example.com,resources=redis,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=redis.example.com,resources=redis/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=redis.example.com,resources=redis/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets;deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=pods;services;configmaps;secrets;persistentvolumeclaims;events,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=monitoring.coreos.com,resources=servicemonitors,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the controller-runtime entry point. It records Prometheus metrics
// for every reconcile invocation and delegates to doReconcile for business logic.
func (r *RedisReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	start := time.Now()
	result, err := r.doReconcile(ctx, req)
	oprmetrics.RecordReconcile(oprmetrics.KindRedis, start, err)
	return result, err
}

func (r *RedisReconciler) doReconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	redisObj := &redisv1alpha1.Redis{}
	if err := r.Get(ctx, req.NamespacedName, redisObj); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !redisObj.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, redisObj)
	}

	if !controllerutil.ContainsFinalizer(redisObj, redisv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(redisObj, redisv1alpha1.FinalizerName)
		if err := r.Update(ctx, redisObj); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	if redisObj.Status.Phase == "" {
		redisObj.Status.Phase = redisv1alpha1.PhaseInitializing
		if err := r.Status().Update(ctx, redisObj); err != nil {
			return ctrl.Result{}, err
		}
	}

	configHash, err := r.reconcileConfigMap(ctx, redisObj)
	if err != nil {
		logger.Error(err, "failed to reconcile ConfigMap")
		r.setFailedStatus(ctx, redisObj, "ConfigMapFailed", err.Error())
		return ctrl.Result{}, err
	}

	if redisObj.Spec.Storage != nil {
		if err := r.reconcileHeadlessService(ctx, redisObj); err != nil {
			logger.Error(err, "failed to reconcile HeadlessService")
			return ctrl.Result{}, err
		}
		if err := r.reconcileStatefulSet(ctx, redisObj, configHash); err != nil {
			logger.Error(err, "failed to reconcile StatefulSet")
			return ctrl.Result{}, err
		}
	} else {
		if err := r.reconcileDeployment(ctx, redisObj, configHash); err != nil {
			logger.Error(err, "failed to reconcile Deployment")
			return ctrl.Result{}, err
		}
	}

	if err := r.reconcileService(ctx, redisObj); err != nil {
		logger.Error(err, "failed to reconcile Service")
		return ctrl.Result{}, err
	}

	if redisObj.Spec.EnableExporter != nil && *redisObj.Spec.EnableExporter {
		if err := r.reconcileServiceMonitor(ctx, redisObj); err != nil {
			// Non-fatal — prometheus-operator may not be installed in this cluster.
			logger.V(1).Info("ServiceMonitor reconcile skipped", "reason", err.Error())
		}
	}

	r.tryHotReload(ctx, redisObj)

	if err := r.updateStatus(ctx, redisObj); err != nil {
		logger.Error(err, "failed to update status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

func (r *RedisReconciler) handleDeletion(ctx context.Context, redisObj *redisv1alpha1.Redis) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(redisObj, redisv1alpha1.FinalizerName) {
		return ctrl.Result{}, nil
	}

	if redisObj.Spec.Storage != nil && !redisObj.Spec.Storage.KeepAfterDeletion {
		pvc := &corev1.PersistentVolumeClaim{}
		pvcKey := client.ObjectKey{Name: "data-" + redisObj.Name + "-0", Namespace: redisObj.Namespace}
		if err := r.Get(ctx, pvcKey, pvc); err == nil {
			if err := r.Delete(ctx, pvc); err != nil && !errors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
		}
	}

	r.Recorder.Event(redisObj, corev1.EventTypeNormal, "Deleted", "Redis instance cleaned up")
	controllerutil.RemoveFinalizer(redisObj, redisv1alpha1.FinalizerName)
	return ctrl.Result{}, r.Update(ctx, redisObj)
}

// reconcileConfigMap creates/updates the Redis config ConfigMap and returns a hash of its data.
func (r *RedisReconciler) reconcileConfigMap(ctx context.Context, redisObj *redisv1alpha1.Redis) (string, error) {
	desired := resources.BuildRedisConfigMap(redisObj)

	existing := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Data = desired.Data
		return ctrl.SetControllerReference(redisObj, existing, r.Scheme)
	})
	if err != nil {
		return "", err
	}
	return configDataHash(desired.Data), nil
}

// reconcileHeadlessService creates/updates the headless Service needed by the StatefulSet for stable DNS.
func (r *RedisReconciler) reconcileHeadlessService(ctx context.Context, redisObj *redisv1alpha1.Redis) error {
	desired := resources.BuildHeadlessService(redisObj.Name, redisObj.Namespace, resources.ComponentRedis)

	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Annotations = desired.Annotations
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = desired.Spec
		} else {
			existing.Spec.Selector = desired.Spec.Selector
			existing.Spec.Ports = desired.Spec.Ports
			existing.Spec.PublishNotReadyAddresses = desired.Spec.PublishNotReadyAddresses
		}
		return ctrl.SetControllerReference(redisObj, existing, r.Scheme)
	})
	return err
}

// reconcileStatefulSet creates/updates the StatefulSet, injecting the config hash to drive rolling updates.
func (r *RedisReconciler) reconcileStatefulSet(ctx context.Context, redisObj *redisv1alpha1.Redis, configHash string) error {
	desired := resources.BuildRedisStatefulSet(redisObj)
	if desired.Spec.Template.Annotations == nil {
		desired.Spec.Template.Annotations = make(map[string]string)
	}
	desired.Spec.Template.Annotations[configHashAnnotation] = configHash

	existing := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = desired.Spec
		} else {
			existing.Spec.Template = desired.Spec.Template
			existing.Spec.Replicas = desired.Spec.Replicas
		}
		return ctrl.SetControllerReference(redisObj, existing, r.Scheme)
	})
	return err
}

// reconcileDeployment creates/updates the Deployment, injecting the config hash to drive rolling updates.
func (r *RedisReconciler) reconcileDeployment(ctx context.Context, redisObj *redisv1alpha1.Redis, configHash string) error {
	desired := resources.BuildRedisDeployment(redisObj)
	if desired.Spec.Template.Annotations == nil {
		desired.Spec.Template.Annotations = make(map[string]string)
	}
	desired.Spec.Template.Annotations[configHashAnnotation] = configHash

	existing := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = desired.Spec
		} else {
			existing.Spec.Template = desired.Spec.Template
			existing.Spec.Replicas = desired.Spec.Replicas
		}
		return ctrl.SetControllerReference(redisObj, existing, r.Scheme)
	})
	return err
}

// reconcileService creates/updates the client-facing ClusterIP Service.
func (r *RedisReconciler) reconcileService(ctx context.Context, redisObj *redisv1alpha1.Redis) error {
	desired := resources.BuildRedisService(redisObj)

	existing := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		if existing.CreationTimestamp.IsZero() {
			existing.Spec = desired.Spec
		} else {
			// Preserve ClusterIP — Kubernetes rejects changes to it.
			existing.Spec.Selector = desired.Spec.Selector
			existing.Spec.Ports = desired.Spec.Ports
		}
		return ctrl.SetControllerReference(redisObj, existing, r.Scheme)
	})
	return err
}

// reconcileServiceMonitor creates/updates the Prometheus ServiceMonitor.
func (r *RedisReconciler) reconcileServiceMonitor(ctx context.Context, redisObj *redisv1alpha1.Redis) error {
	desired := resources.BuildRedisServiceMonitor(redisObj)

	existing := &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      desired.Name,
			Namespace: desired.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, existing, func() error {
		existing.Labels = desired.Labels
		existing.Spec = desired.Spec
		return ctrl.SetControllerReference(redisObj, existing, r.Scheme)
	})
	return err
}

// tryHotReload pushes hot-reloadable config changes to the running Redis via CONFIG SET.
// Errors are logged but never returned — this is a best-effort optimisation.
func (r *RedisReconciler) tryHotReload(ctx context.Context, redisObj *redisv1alpha1.Redis) {
	if r.RedisClient == nil || redisObj.Spec.RedisConfig == nil {
		return
	}
	logger := log.FromContext(ctx)

	addr := fmt.Sprintf("%s.%s.svc.cluster.local:%d",
		redisObj.Name, redisObj.Namespace, resources.RedisPort)

	// Retrieve password from secret if auth is configured.
	if redisObj.Spec.Auth != nil {
		secret := &corev1.Secret{}
		if err := r.Get(ctx, client.ObjectKey{
			Name:      redisObj.Spec.Auth.SecretName,
			Namespace: redisObj.Namespace,
		}, secret); err != nil {
			logger.V(1).Info("hot-reload: could not read auth secret", "err", err)
			return
		}
	}

	for k, v := range redisObj.Spec.RedisConfig {
		if !hotReloadableParams[k] {
			continue
		}
		if err := r.RedisClient.ConfigSet(ctx, addr, k, v); err != nil {
			logger.V(1).Info("hot-reload: CONFIG SET failed", "key", k, "err", err)
		}
	}
}

// updateStatus synchronises the Redis status subresource with the current cluster state.
func (r *RedisReconciler) updateStatus(ctx context.Context, redisObj *redisv1alpha1.Redis) error {
	podList := &corev1.PodList{}
	if err := r.List(ctx, podList,
		client.InNamespace(redisObj.Namespace),
		client.MatchingLabels(resources.PodLabels(redisObj.Name, resources.ComponentRedis)),
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

	ready := readyCount > 0
	readyCond := metav1.Condition{
		Type:               conditionReady,
		Status:             boolConditionStatus(ready),
		ObservedGeneration: redisObj.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reasonReady,
		Message:            fmt.Sprintf("%d/1 pods ready", readyCount),
	}
	if !ready {
		readyCond.Reason = reasonReconciling
		readyCond.Message = "waiting for pods to become ready"
	}
	apimeta.SetStatusCondition(&redisObj.Status.Conditions, readyCond)

	if ready {
		redisObj.Status.Phase = redisv1alpha1.PhaseReady
	} else if redisObj.Status.Phase != redisv1alpha1.PhaseFailed {
		redisObj.Status.Phase = redisv1alpha1.PhaseInitializing
	}
	redisObj.Status.ReadyReplicas = readyCount

	return r.Status().Update(ctx, redisObj)
}

func (r *RedisReconciler) setFailedStatus(ctx context.Context, redisObj *redisv1alpha1.Redis, reason, msg string) {
	apimeta.SetStatusCondition(&redisObj.Status.Conditions, metav1.Condition{
		Type:               conditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: redisObj.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            msg,
	})
	redisObj.Status.Phase = redisv1alpha1.PhaseFailed
	_ = r.Status().Update(ctx, redisObj)
}

// configDataHash returns a 16-char hex prefix of the SHA-256 hash of sorted ConfigMap data.
func configDataHash(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		fmt.Fprintf(h, "%s=%s\n", k, data[k])
	}
	return fmt.Sprintf("%x", h.Sum(nil))[:16]
}

func boolConditionStatus(ok bool) metav1.ConditionStatus {
	if ok {
		return metav1.ConditionTrue
	}
	return metav1.ConditionFalse
}

// SetupWithManager registers the controller with the Manager.
func (r *RedisReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&redisv1alpha1.Redis{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
