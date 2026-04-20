package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisSentinelSpec defines a Redis deployment with Sentinel-based high availability.
type RedisSentinelSpec struct {
	// Replicas is the total number of Redis pods (1 master + N-1 replicas).
	// +kubebuilder:validation:Minimum=3
	// +kubebuilder:default=3
	Replicas int32 `json:"replicas"`

	// SentinelReplicas is the number of Sentinel pods. Must be odd and at least 3.
	// +kubebuilder:validation:Minimum=3
	// +kubebuilder:default=3
	SentinelReplicas int32 `json:"sentinelReplicas"`

	// Image is the Redis container image.
	// +kubebuilder:default="redis:7.2-alpine"
	// +optional
	Image string `json:"image,omitempty"`

	// SentinelImage is the Sentinel container image. Defaults to Image when empty.
	// +optional
	SentinelImage string `json:"sentinelImage,omitempty"`

	// Resources defines compute resource requirements for Redis containers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// SentinelResources defines compute resource requirements for Sentinel containers.
	// +optional
	SentinelResources corev1.ResourceRequirements `json:"sentinelResources,omitempty"`

	// Storage configures persistent storage for Redis data.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// RedisConfig contains custom redis.conf key-value pairs.
	// +optional
	RedisConfig map[string]string `json:"redisConfig,omitempty"`

	// SentinelConfig contains custom sentinel.conf key-value pairs.
	// +optional
	SentinelConfig map[string]string `json:"sentinelConfig,omitempty"`

	// Auth references a Kubernetes Secret holding the Redis password.
	// +optional
	Auth *AuthSpec `json:"auth,omitempty"`

	// TLS configures TLS termination.
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

	// Affinity defines pod scheduling affinity rules for Redis pods.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations defines pod scheduling tolerations for Redis pods.
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// NodeSelector defines node selection constraints for Redis pods.
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
}

// RedisSentinelStatus defines the observed state of RedisSentinel.
type RedisSentinelStatus struct {
	// Phase is the current lifecycle phase.
	Phase string `json:"phase"`

	// Message is a human-readable description of the current state.
	// +optional
	Message string `json:"message,omitempty"`

	// Conditions follow the Kubernetes condition convention.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// MasterNode is the pod name of the current Redis master.
	// +optional
	MasterNode string `json:"masterNode,omitempty"`

	// ReadyReplicas is the number of Redis pods in Ready state.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// ReadySentinels is the number of Sentinel pods in Ready state.
	// +optional
	ReadySentinels int32 `json:"readySentinels,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=redissentinel;rss
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Master",type=string,JSONPath=`.status.masterNode`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Sentinels",type=integer,JSONPath=`.status.readySentinels`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RedisSentinel is the Schema for the redissentinels API.
type RedisSentinel struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedisSentinelSpec   `json:"spec,omitempty"`
	Status RedisSentinelStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedisSentinelList contains a list of RedisSentinel.
type RedisSentinelList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedisSentinel `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RedisSentinel{}, &RedisSentinelList{})
}
