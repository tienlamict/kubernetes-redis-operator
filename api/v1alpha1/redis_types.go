package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisSpec defines the desired state of a standalone Redis instance.
type RedisSpec struct {
	// Image is the Redis container image.
	// +kubebuilder:default="redis:7.2-alpine"
	// +optional
	Image string `json:"image,omitempty"`

	// Resources defines compute resource requirements for the Redis container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Storage configures persistent storage for Redis data.
	// When set, a StatefulSet with a PVC is created instead of a Deployment.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// RedisConfig contains custom redis.conf key-value pairs that override defaults.
	// Example: {"maxmemory": "256mb", "maxmemory-policy": "allkeys-lru"}
	// +optional
	RedisConfig map[string]string `json:"redisConfig,omitempty"`

	// Auth references a Kubernetes Secret holding the Redis password.
	// +optional
	Auth *AuthSpec `json:"auth,omitempty"`

	// TLS configures TLS termination for client connections.
	// +optional
	TLS *TLSSpec `json:"tls,omitempty"`

	// EnableExporter controls whether a Prometheus redis_exporter sidecar is injected.
	// +kubebuilder:default=true
	// +optional
	EnableExporter *bool `json:"enableExporter,omitempty"`

	// ExporterImage is the image used for the Prometheus exporter sidecar.
	// +kubebuilder:default="oliver006/redis_exporter:latest"
	// +optional
	ExporterImage string `json:"exporterImage,omitempty"`

	// Affinity defines pod scheduling affinity rules.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations defines pod scheduling tolerations.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// NodeSelector defines a map of key-value pairs for pod node selection.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// RedisStatus defines the observed state of Redis.
type RedisStatus struct {
	// Phase is the current lifecycle phase: Initializing, Ready, or Failed.
	Phase string `json:"phase"`

	// Message is a human-readable description of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions follow the Kubernetes condition convention.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// RedisVersion is the Redis server version detected at runtime.
	// +optional
	RedisVersion string `json:"redisVersion,omitempty"`

	// ReadyReplicas is the number of pods in Ready state.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=redis
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Redis is the Schema for the redis API.
type Redis struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedisSpec   `json:"spec,omitempty"`
	Status RedisStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedisList contains a list of Redis.
type RedisList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Redis `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Redis{}, &RedisList{})
}
