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

// StatefulSetName returns the conventional StatefulSet name for a given CR and component.
func StatefulSetName(crName, component string) string {
	if component == ComponentRedis || component == ComponentMaster {
		return crName
	}
	return fmt.Sprintf("%s-%s", crName, component)
}

// HeadlessServiceName returns the headless Service name for a StatefulSet.
func HeadlessServiceName(crName, component string) string {
	return fmt.Sprintf("%s-%s-headless", crName, component)
}

// PodFQDN returns the fully-qualified DNS name for a StatefulSet pod.
func PodFQDN(podName, headlessSvcName, namespace string) string {
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, headlessSvcName, namespace)
}
