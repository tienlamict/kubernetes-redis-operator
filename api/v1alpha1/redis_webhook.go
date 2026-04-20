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

var redislog = logf.Log.WithName("redis-resource")

// SetupWebhookWithManager registers this type's webhook handlers with the manager.
func (r *Redis) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-redis-example-com-v1alpha1-redis,mutating=true,failurePolicy=fail,sideEffects=None,groups=redis.example.com,resources=redis,verbs=create;update,versions=v1alpha1,name=mredis.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &Redis{}

// Default applies default values to a Redis resource on create/update.
func (r *Redis) Default() {
	redislog.Info("applying defaults", "name", r.Name)

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
}

// +kubebuilder:webhook:path=/validate-redis-example-com-v1alpha1-redis,mutating=false,failurePolicy=fail,sideEffects=None,groups=redis.example.com,resources=redis,verbs=create;update,versions=v1alpha1,name=vredis.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &Redis{}

// ValidateCreate validates a newly created Redis resource.
func (r *Redis) ValidateCreate() (admission.Warnings, error) {
	redislog.Info("validate create", "name", r.Name)
	return r.validateSpec()
}

// ValidateUpdate validates an update to an existing Redis resource.
func (r *Redis) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	redislog.Info("validate update", "name", r.Name)

	oldRedis, ok := old.(*Redis)
	if !ok {
		return nil, fmt.Errorf("unexpected type %T", old)
	}

	if _, err := r.validateSpec(); err != nil {
		return nil, err
	}

	// Prevent adding storage to an existing instance (would require StatefulSet migration).
	if oldRedis.Spec.Storage == nil && r.Spec.Storage != nil {
		return nil, fmt.Errorf("cannot add storage to an existing Redis instance; delete and recreate with storage configured")
	}

	// Prevent removing storage from an existing instance.
	if oldRedis.Spec.Storage != nil && r.Spec.Storage == nil {
		return nil, fmt.Errorf("cannot remove storage from an existing Redis instance")
	}

	if oldRedis.Spec.Storage != nil && r.Spec.Storage != nil {
		// Prevent storage class change.
		oldClass := ""
		if oldRedis.Spec.Storage.ClassName != nil {
			oldClass = *oldRedis.Spec.Storage.ClassName
		}
		newClass := ""
		if r.Spec.Storage.ClassName != nil {
			newClass = *r.Spec.Storage.ClassName
		}
		if oldClass != newClass {
			return nil, fmt.Errorf("spec.storage.storageClassName is immutable after creation")
		}

		// Prevent storage size shrink.
		oldQty, err1 := resource.ParseQuantity(oldRedis.Spec.Storage.Size)
		newQty, err2 := resource.ParseQuantity(r.Spec.Storage.Size)
		if err1 == nil && err2 == nil && newQty.Cmp(oldQty) < 0 {
			return nil, fmt.Errorf("spec.storage.size cannot be decreased (from %s to %s)",
				oldRedis.Spec.Storage.Size, r.Spec.Storage.Size)
		}
	}

	return nil, nil
}

// ValidateDelete validates a deletion request.
func (r *Redis) ValidateDelete() (admission.Warnings, error) {
	redislog.Info("validate delete", "name", r.Name)
	return nil, nil
}

// validateSpec checks the spec fields for both create and update paths.
func (r *Redis) validateSpec() (admission.Warnings, error) {
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
