package v1alpha1

// StorageSpec defines persistent storage configuration for a Redis instance.
type StorageSpec struct {
	// ClassName is the StorageClass name for the PVC.
	// +optional
	ClassName *string `json:"className,omitempty"`

	// Size is the requested storage size (e.g., "10Gi").
	// +kubebuilder:validation:Required
	Size string `json:"size"`

	// KeepAfterDeletion prevents PVC deletion when the Redis CR is deleted.
	// +optional
	KeepAfterDeletion bool `json:"keepAfterDeletion,omitempty"`
}

// AuthSpec references a Kubernetes Secret that contains the Redis password.
type AuthSpec struct {
	// SecretName is the name of the Secret containing a "password" key.
	// +kubebuilder:validation:Required
	SecretName string `json:"secretName"`
}

// TLSSpec references a Kubernetes Secret that contains TLS certificates.
type TLSSpec struct {
	// SecretName is the name of the Secret containing tls.crt, tls.key, and ca.crt.
	// +kubebuilder:validation:Required
	SecretName string `json:"secretName"`
}

const (
	// FinalizerName is the finalizer added to all Redis CRs.
	FinalizerName = "redis.example.com/finalizer"

	// PhaseInitializing indicates the resource is being created.
	PhaseInitializing = "Initializing"
	// PhaseReady indicates the resource is fully operational.
	PhaseReady = "Ready"
	// PhaseFailed indicates an unrecoverable error.
	PhaseFailed = "Failed"
	// PhaseScaling indicates a cluster is undergoing a scale operation.
	PhaseScaling = "Scaling"
	// PhaseRecovering indicates a cluster is recovering from a failure.
	PhaseRecovering = "Recovering"

	// DefaultRedisImage is the default Redis container image.
	DefaultRedisImage = "redis:7.2-alpine"
	// DefaultExporterImage is the default Prometheus exporter sidecar image.
	DefaultExporterImage = "oliver006/redis_exporter:latest"

	// LabelManagedBy is the standard "managed-by" label value.
	LabelManagedBy = "redis-operator"
	// LabelPartOf is the standard "part-of" label value.
	LabelPartOf = "redis"
)
