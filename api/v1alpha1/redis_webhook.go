package v1alpha1

import (
	"fmt"

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

// Default applies default values to Redis when it is created or updated.
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

	if r.Spec.Storage != nil && r.Spec.Storage.Size == "" {
		return nil, fmt.Errorf("spec.storage.size must not be empty when storage is configured")
	}
	return nil, nil
}

// ValidateUpdate validates an update to an existing Redis resource.
func (r *Redis) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	redislog.Info("validate update", "name", r.Name)
	return r.ValidateCreate()
}

// ValidateDelete validates a deletion request.
func (r *Redis) ValidateDelete() (admission.Warnings, error) {
	redislog.Info("validate delete", "name", r.Name)
	return nil, nil
}
