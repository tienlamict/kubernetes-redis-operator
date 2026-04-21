// Package resources provides builders for the Kubernetes resources managed by the Redis operator.
package resources

import (
	"fmt"
)

const (
	labelName      = "app.kubernetes.io/name"
	labelInstance  = "app.kubernetes.io/instance"
	labelComponent = "app.kubernetes.io/component"
	labelManagedBy = "app.kubernetes.io/managed-by"
	labelPartOf    = "app.kubernetes.io/part-of"
	labelCluster   = "redis.example.com/cluster"

	ComponentMaster   = "master"
	ComponentReplica  = "replica"
	ComponentSentinel = "sentinel"
	ComponentRedis    = "redis"

	// RoleLabelKey is a per-pod label managed by the controller that tracks the live
	// Sentinel role of each Redis pod.  Services select on this label so that
	// master/replica routing stays correct even after a Sentinel failover.
	RoleLabelKey = "redis.example.com/role"
	// RoleMaster is the role label value for the current Sentinel master pod.
	RoleMaster = "master"
	// RoleReplica is the role label value for Sentinel replica pods.
	RoleReplica = "replica"
)

// CommonLabels returns the standard set of labels for operator-owned resources.
func CommonLabels(name, component string) map[string]string {
	return map[string]string{
		labelName:      "redis",
		labelInstance:  name,
		labelComponent: component,
		labelManagedBy: "redis-operator",
		labelPartOf:    "redis",
		labelCluster:   name,
	}
}

// PodLabels returns labels used on pods (superset of CommonLabels, used for Service selectors).
func PodLabels(name, component string) map[string]string {
	return CommonLabels(name, component)
}

// StatefulSetName returns the conventional StatefulSet name for standalone and sentinel CRs.
// For cluster CRs use ClusterMasterStatefulSetName / ClusterReplicaStatefulSetName instead.
func StatefulSetName(crName, component string) string {
	if component == ComponentRedis || component == ComponentMaster {
		return crName
	}
	return fmt.Sprintf("%s-%s", crName, component)
}

// ClusterMasterStatefulSetName returns the StatefulSet name for RedisCluster master pods.
// Kept separate from StatefulSetName to avoid a name collision with the client Service,
// which is also named after the CR.
func ClusterMasterStatefulSetName(crName string) string {
	return crName + "-masters"
}

// ClusterReplicaStatefulSetName returns the StatefulSet name for RedisCluster replica pods.
func ClusterReplicaStatefulSetName(crName string) string {
	return crName + "-replicas"
}

// HeadlessServiceName returns the headless Service name for a StatefulSet.
func HeadlessServiceName(crName, component string) string {
	return fmt.Sprintf("%s-%s-headless", crName, component)
}

// PodFQDN returns the fully-qualified DNS name for a StatefulSet pod.
func PodFQDN(podName, headlessSvcName, namespace string) string {
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, headlessSvcName, namespace)
}
