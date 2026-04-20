package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RedisClusterSpec defines a native Redis Cluster with hash slot sharding.
type RedisClusterSpec struct {
	// Masters is the number of master nodes. Must be at least 3.
	// +kubebuilder:validation:Minimum=3
	// +kubebuilder:default=3
	Masters int32 `json:"masters"`

	// ReplicasPerMaster is the number of replica nodes per master.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:default=1
	ReplicasPerMaster int32 `json:"replicasPerMaster"`

	// Image is the Redis container image.
	// +kubebuilder:default="redis:7.2-alpine"
	// +optional
	Image string `json:"image,omitempty"`

	// Resources defines the default compute resource requirements for all Redis nodes.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// MasterResources overrides Resources for master nodes specifically.
	// +optional
	MasterResources *corev1.ResourceRequirements `json:"masterResources,omitempty"`

	// ReplicaResources overrides Resources for replica nodes specifically.
	// +optional
	ReplicaResources *corev1.ResourceRequirements `json:"replicaResources,omitempty"`

	// Storage configures persistent storage for Redis data and nodes.conf.
	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// RedisConfig contains custom redis.conf key-value pairs applied to all nodes.
	// +optional
	RedisConfig map[string]string `json:"redisConfig,omitempty"`

	// Auth references a Kubernetes Secret holding the Redis password.
	// +optional
	Auth *AuthSpec `json:"auth,omitempty"`

	// TLS configures TLS for cluster bus and client connections.
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

	// MasterAffinity defines pod scheduling affinity rules for master pods.
	// +optional
	MasterAffinity *corev1.Affinity `json:"masterAffinity,omitempty"`

	// MasterTolerations defines pod scheduling tolerations for master pods.
	// +optional
	MasterTolerations []corev1.Toleration `json:"masterTolerations,omitempty"`

	// MasterNodeSelector defines node selection constraints for master pods.
	// +optional
	MasterNodeSelector map[string]string `json:"masterNodeSelector,omitempty"`

	// ReplicaAffinity defines pod scheduling affinity rules for replica pods.
	// +optional
	ReplicaAffinity *corev1.Affinity `json:"replicaAffinity,omitempty"`

	// ReplicaTolerations defines pod scheduling tolerations for replica pods.
	// +optional
	ReplicaTolerations []corev1.Toleration `json:"replicaTolerations,omitempty"`

	// ReplicaNodeSelector defines node selection constraints for replica pods.
	// +optional
	ReplicaNodeSelector map[string]string `json:"replicaNodeSelector,omitempty"`
}

// RedisClusterStatus defines the observed state of RedisCluster.
type RedisClusterStatus struct {
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

	// ReadyMasters is the number of master nodes in Ready state.
	// +optional
	ReadyMasters int32 `json:"readyMasters,omitempty"`

	// ReadyReplicas is the number of replica nodes in Ready state.
	// +optional
	ReadyReplicas int32 `json:"readyReplicas,omitempty"`

	// AssignedSlots is the total number of hash slots assigned across all masters.
	// A healthy cluster has 16384.
	// +optional
	AssignedSlots int32 `json:"assignedSlots,omitempty"`

	// ClusterState is the Redis CLUSTER INFO cluster_state value: "ok" or "fail".
	// +optional
	ClusterState string `json:"clusterState,omitempty"`

	// Nodes contains per-node status information.
	// +optional
	Nodes []RedisNodeStatus `json:"nodes,omitempty"`
}

// RedisNodeStatus holds the status of a single node within a RedisCluster.
type RedisNodeStatus struct {
	// NodeID is the 40-character hex Redis node identifier.
	NodeID string `json:"nodeID"`

	// PodName is the Kubernetes pod name hosting this node.
	PodName string `json:"podName"`

	// IP is the pod IP address.
	IP string `json:"ip"`

	// Role is either "master" or "replica".
	Role string `json:"role"`

	// Slots lists the hash slot ranges owned by this master node (e.g., "0-5460").
	// +optional
	Slots string `json:"slots,omitempty"`

	// MasterRef is the NodeID of the master this replica node replicates.
	// +optional
	MasterRef string `json:"masterRef,omitempty"`

	// Health is the node health status as reported by CLUSTER NODES.
	Health string `json:"health"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=rediscluster;rsc
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Masters",type=integer,JSONPath=`.status.readyMasters`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.status.readyReplicas`
// +kubebuilder:printcolumn:name="Slots",type=integer,JSONPath=`.status.assignedSlots`
// +kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.clusterState`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// RedisCluster is the Schema for the redisclusters API.
type RedisCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   RedisClusterSpec   `json:"spec,omitempty"`
	Status RedisClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RedisClusterList contains a list of RedisCluster.
type RedisClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RedisCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RedisCluster{}, &RedisClusterList{})
}
