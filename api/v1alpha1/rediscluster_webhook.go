package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var redisclusterlog = logf.Log.WithName("rediscluster-resource")

// SetupWebhookWithManager registers this type's webhook handlers with the manager.
func (r *RedisCluster) SetupWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr).
		For(r).
		Complete()
}

// +kubebuilder:webhook:path=/mutate-redis-example-com-v1alpha1-rediscluster,mutating=true,failurePolicy=fail,sideEffects=None,groups=redis.example.com,resources=redisclusters,verbs=create;update,versions=v1alpha1,name=mrediscluster.kb.io,admissionReviewVersions=v1

var _ webhook.Defaulter = &RedisCluster{}

// Default applies default values to RedisCluster when it is created or updated.
func (r *RedisCluster) Default() {
	redisclusterlog.Info("applying defaults", "name", r.Name)

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
	if r.Spec.Masters == 0 {
		r.Spec.Masters = 3
	}
	// Apply default cluster-specific redis.conf values.
	if r.Spec.RedisConfig == nil {
		r.Spec.RedisConfig = make(map[string]string)
	}
	if _, ok := r.Spec.RedisConfig["cluster-node-timeout"]; !ok {
		r.Spec.RedisConfig["cluster-node-timeout"] = "15000"
	}
	if _, ok := r.Spec.RedisConfig["appendonly"]; !ok {
		r.Spec.RedisConfig["appendonly"] = "yes"
	}
}

// +kubebuilder:webhook:path=/validate-redis-example-com-v1alpha1-rediscluster,mutating=false,failurePolicy=fail,sideEffects=None,groups=redis.example.com,resources=redisclusters,verbs=create;update;delete,versions=v1alpha1,name=vrediscluster.kb.io,admissionReviewVersions=v1

var _ webhook.Validator = &RedisCluster{}

// ValidateCreate validates a newly created RedisCluster resource.
func (r *RedisCluster) ValidateCreate() (admission.Warnings, error) {
	redisclusterlog.Info("validate create", "name", r.Name)

	if r.Spec.Masters < 3 {
		return nil, fmt.Errorf("spec.masters must be at least 3, got %d", r.Spec.Masters)
	}
	if r.Spec.ReplicasPerMaster < 0 {
		return nil, fmt.Errorf("spec.replicasPerMaster must be >= 0, got %d", r.Spec.ReplicasPerMaster)
	}
	totalNodes := r.Spec.Masters + r.Spec.Masters*r.Spec.ReplicasPerMaster
	if totalNodes > 100 {
		return nil, fmt.Errorf("total node count %d exceeds the limit of 100", totalNodes)
	}

	var warnings admission.Warnings
	if r.Spec.Masters%2 == 0 {
		warnings = append(warnings, fmt.Sprintf("spec.masters=%d is even; an odd number is recommended for quorum", r.Spec.Masters))
	}
	return warnings, nil
}

// ValidateUpdate validates an update to an existing RedisCluster resource.
func (r *RedisCluster) ValidateUpdate(old runtime.Object) (admission.Warnings, error) {
	redisclusterlog.Info("validate update", "name", r.Name)

	oldCluster, ok := old.(*RedisCluster)
	if !ok {
		return nil, fmt.Errorf("expected a RedisCluster object for the old resource")
	}
	if r.Spec.Masters < 3 {
		return nil, fmt.Errorf("cannot scale spec.masters below 3")
	}
	if oldCluster.Spec.Masters > r.Spec.Masters && (oldCluster.Spec.Masters-r.Spec.Masters) > 1 {
		return nil, fmt.Errorf("cannot scale masters down by more than 1 at a time (current: %d, requested: %d)",
			oldCluster.Spec.Masters, r.Spec.Masters)
	}
	if oldCluster.Status.Phase == PhaseScaling {
		return nil, fmt.Errorf("cannot delete or modify cluster while it is in %s phase", PhaseScaling)
	}
	return r.ValidateCreate()
}

// ValidateDelete validates a deletion request.
func (r *RedisCluster) ValidateDelete() (admission.Warnings, error) {
	redisclusterlog.Info("validate delete", "name", r.Name)
	if r.Status.Phase == PhaseScaling {
		return nil, fmt.Errorf("cannot delete cluster while it is in %s phase", PhaseScaling)
	}
	return nil, nil
}
