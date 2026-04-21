package metrics

import "time"

// Kind label constants used in reconcile metrics.
const (
	KindRedis    = "Redis"
	KindSentinel = "RedisSentinel"
	KindCluster  = "RedisCluster"
)

// RecordReconcile bumps the reconcile counter and observes duration.
// Pass the start time captured at the top of the Reconcile entry point and
// the error value (nil on success) returned by the inner reconcile logic.
func RecordReconcile(kind string, start time.Time, err error) {
	result := "success"
	if err != nil {
		result = "error"
		ReconcileErrors.WithLabelValues(kind, "error").Inc()
	}
	ReconcileTotal.WithLabelValues(kind, result).Inc()
	ReconcileDuration.WithLabelValues(kind).Observe(time.Since(start).Seconds())
}

// SetClusterMetrics updates all per-cluster health gauges.
// Call this once per reconcile after reading cluster state.
// state is the Redis CLUSTER INFO cluster_state value ("ok" or "fail" or "unknown").
func SetClusterMetrics(name, namespace, state string, slots, nodes, masters, replicas int32) {
	healthy := float64(0)
	if state == "ok" {
		healthy = 1
	}
	ClusterState.WithLabelValues(name, namespace).Set(healthy)
	ClusterAssignedSlots.WithLabelValues(name, namespace).Set(float64(slots))
	ClusterKnownNodes.WithLabelValues(name, namespace).Set(float64(nodes))
	ClusterMasterCount.WithLabelValues(name, namespace).Set(float64(masters))
	ClusterReplicaCount.WithLabelValues(name, namespace).Set(float64(replicas))
}

// RecordClusterScaling records a completed scale-out or scale-in operation.
// direction must be "out" or "in".
// Pass the time captured at the start of the scaling operation.
func RecordClusterScaling(name, namespace, direction string, start time.Time) {
	ClusterScalingOperations.WithLabelValues(name, namespace, direction).Inc()
	ClusterScalingDuration.WithLabelValues(name, namespace, direction).Observe(time.Since(start).Seconds())
}

// RecordClusterFailover increments the failover event counter for a cluster.
// Call it once when the controller first detects the cluster entering a fail state.
func RecordClusterFailover(name, namespace string) {
	ClusterFailoverTotal.WithLabelValues(name, namespace).Inc()
}

// DeleteClusterMetrics removes all per-label-set gauge series for a deleted
// RedisCluster so stale time-series are not exported after CR deletion.
func DeleteClusterMetrics(name, namespace string) {
	ClusterState.DeleteLabelValues(name, namespace)
	ClusterAssignedSlots.DeleteLabelValues(name, namespace)
	ClusterKnownNodes.DeleteLabelValues(name, namespace)
	ClusterMasterCount.DeleteLabelValues(name, namespace)
	ClusterReplicaCount.DeleteLabelValues(name, namespace)
}
