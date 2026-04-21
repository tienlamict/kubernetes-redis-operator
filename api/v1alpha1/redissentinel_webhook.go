package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
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
	return r.validateSentinelSpec()
}

// ValidateUpdate validates an update to an existing RedisSentinel resource.
func (r *RedisSentinel) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	redissentinellog.Info("validate update", "name", r.Name)

	oldSentinel, ok := old.(*RedisSentinel)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T for old object", old)
	}

	// Validate the new spec first.
	if _, err := r.validateSentinelSpec(); err != nil {
		return nil, err
	}

	// Prevent scaling Redis replicas below 3.
	if r.Spec.Replicas < 3 {
		return nil, fmt.Errorf("cannot scale spec.replicas below 3, got %d", r.Spec.Replicas)
	}
	// Prevent scaling down Sentinel replicas below 3.
	if r.Spec.SentinelReplicas < 3 {
		return nil, fmt.Errorf("cannot scale spec.sentinelReplicas below 3, got %d", r.Spec.SentinelReplicas)
	}

	// Storage immutability checks.
	if oldSentinel.Spec.Storage == nil && r.Spec.Storage != nil {
		return nil, fmt.Errorf("cannot add storage to an existing RedisSentinel instance")
	}
	if oldSentinel.Spec.Storage != nil && r.Spec.Storage == nil {
		return nil, fmt.Errorf("cannot remove storage from an existing RedisSentinel instance")
	}
	if oldSentinel.Spec.Storage != nil && r.Spec.Storage != nil {
		oldClass := ""
		if oldSentinel.Spec.Storage.ClassName != nil {
			oldClass = *oldSentinel.Spec.Storage.ClassName
		}
		newClass := ""
		if r.Spec.Storage.ClassName != nil {
			newClass = *r.Spec.Storage.ClassName
		}
		if oldClass != newClass {
			return nil, fmt.Errorf("spec.storage.storageClassName is immutable after creation")
		}

		oldQty, err1 := resource.ParseQuantity(oldSentinel.Spec.Storage.Size)
		newQty, err2 := resource.ParseQuantity(r.Spec.Storage.Size)
		if err1 == nil && err2 == nil && newQty.Cmp(oldQty) < 0 {
			return nil, fmt.Errorf("spec.storage.size cannot be decreased (from %s to %s)",
				oldSentinel.Spec.Storage.Size, r.Spec.Storage.Size)
		}
	}

	return nil, nil
}

// ValidateDelete validates a deletion request.
func (r *RedisSentinel) ValidateDelete() (admission.Warnings, error) {
	redissentinellog.Info("validate delete", "name", r.Name)
	return nil, nil
}

// validateSentinelSpec validates the static constraints on the sentinel spec.
func (r *RedisSentinel) validateSentinelSpec() (admission.Warnings, error) {
	if r.Spec.Replicas < 3 {
		return nil, fmt.Errorf("spec.replicas must be at least 3, got %d", r.Spec.Replicas)
	}
	if r.Spec.SentinelReplicas < 3 {
		return nil, fmt.Errorf("spec.sentinelReplicas must be at least 3, got %d", r.Spec.SentinelReplicas)
	}
	if r.Spec.SentinelReplicas%2 == 0 {
		return nil, fmt.Errorf("spec.sentinelReplicas must be odd for quorum, got %d", r.Spec.SentinelReplicas)
	}
	if r.Spec.Storage != nil {
		if r.Spec.Storage.Size == "" {
			return nil, fmt.Errorf("spec.storage.size must not be empty when storage is configured")
		}
		if _, err := resource.ParseQuantity(r.Spec.Storage.Size); err != nil {
			return nil, fmt.Errorf("spec.storage.size %q is not a valid resource.Quantity: %w", r.Spec.Storage.Size, err)
		}
	}
	return nil, nil
}
