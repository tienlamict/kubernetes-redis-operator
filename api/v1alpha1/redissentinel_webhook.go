package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var redissentinellog = logf.Log.WithName("redissentinel-resource")

// SetupWebhookWithManager registers this type's webhook handlers with the manager.
func (r *RedisSentinel) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-redis-example-com-v1alpha1-redissentinel,mutating=true,failurePolicy=fail,sideEffects=None,groups=redis.example.com,resources=redissentinels,verbs=create;update,versions=v1alpha1,name=mredissentinel.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &RedisSentinel{}

// Default applies default values to RedisSentinel when it is created or updated.
func (r *RedisSentinel) Default() {
	redissentinellog.Info("applying defaults", "name", r.Name)

	if r.Spec.Image == "" {
		r.Spec.Image = DefaultRedisImage
	}
	if r.Spec.ExporterImage == "" {
		r.Spec.ExporterImage = DefaultExporterImage
	}
	if r.Spec.EnableExporter == nil {
		t := true
		r.Spec.EnableExporter = &t
	}
	if r.Spec.Replicas == 0 {
		r.Spec.Replicas = 3
	}
	if r.Spec.SentinelReplicas == 0 {
		r.Spec.SentinelReplicas = 3
	}
	if r.Spec.SentinelImage == "" {
		r.Spec.SentinelImage = r.Spec.Image
	}
}

// +kubebuilder:webhook:path=/validate-redis-example-com-v1alpha1-redissentinel,mutating=false,failurePolicy=fail,sideEffects=None,groups=redis.example.com,resources=redissentinels,verbs=create;update,versions=v1alpha1,name=vredissentinel.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &RedisSentinel{}

// ValidateCreate validates a newly created RedisSentinel resource.
func (r *RedisSentinel) ValidateCreate() (admission.Warnings, error) {
	redissentinellog.Info("validate create", "name", r.Name)

	if r.Spec.Replicas < 3 {
		return nil, fmt.Errorf("spec.replicas must be at least 3, got %d", r.Spec.Replicas)
	}
	if r.Spec.SentinelReplicas < 3 {
		return nil, fmt.Errorf("spec.sentinelReplicas must be at least 3, got %d", r.Spec.SentinelReplicas)
	}
	if r.Spec.SentinelReplicas%2 == 0 {
		return nil, fmt.Errorf("spec.sentinelReplicas must be odd for quorum, got %d", r.Spec.SentinelReplicas)
	}
	return nil, nil
}

// ValidateUpdate validates an update to an existing RedisSentinel resource.
func (r *RedisSentinel) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	redissentinellog.Info("validate update", "name", r.Name)
	oldSentinel, ok := old.(*RedisSentinel)
	if ok && r.Spec.Replicas < 3 && r.Spec.Replicas < oldSentinel.Spec.Replicas {
		return nil, fmt.Errorf("cannot scale spec.replicas below 3")
	}
	return r.ValidateCreate()
}

// ValidateDelete validates a deletion request.
func (r *RedisSentinel) ValidateDelete() (admission.Warnings, error) {
	redissentinellog.Info("validate delete", "name", r.Name)
	return nil, nil
}
