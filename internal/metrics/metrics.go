// Package metrics registers custom Prometheus metrics for the Redis operator.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ReconcileTotal counts reconciliation attempts broken down by kind and result.
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "redis_operator_reconcile_total",
			Help: "Total number of reconciliation attempts.",
		},
		[]string{"kind", "result"},
	)

	// ReconcileDuration observes how long each reconciliation takes.
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "redis_operator_reconcile_duration_seconds",
			Help:    "Duration of reconciliation loops in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"kind"},
	)

	// ReconcileErrors counts reconciliation errors broken down by kind and error type.
	ReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "redis_operator_reconcile_errors_total",
			Help: "Total number of reconciliation errors.",
		},
		[]string{"kind", "error_type"},
	)

	// ManagedClusters tracks the number of RedisCluster CRs managed by this operator.
	ManagedClusters = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_operator_managed_clusters_total",
		Help: "Total number of RedisCluster resources managed.",
	})

	// ManagedSentinels tracks the number of RedisSentinel CRs managed by this operator.
	ManagedSentinels = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_operator_managed_sentinels_total",
		Help: "Total number of RedisSentinel resources managed.",
	})

	// ManagedStandalone tracks the number of standalone Redis CRs managed by this operator.
	ManagedStandalone = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "redis_operator_managed_standalone_total",
		Help: "Total number of standalone Redis resources managed.",
	})

	// --- Cluster-level metrics ---

	// ClusterState reports whether a cluster is healthy (1) or not (0).
	ClusterState = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "redis_cluster_state",
			Help: "Redis cluster state: 1 = ok, 0 = fail.",
		},
		[]string{"name", "namespace"},
	)

	// ClusterAssignedSlots tracks the number of assigned hash slots per cluster.
	ClusterAssignedSlots = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "redis_cluster_assigned_slots",
			Help: "Number of hash slots assigned in the Redis cluster.",
		},
		[]string{"name", "namespace"},
	)

	// ClusterKnownNodes tracks the number of nodes known to the cluster.
	ClusterKnownNodes = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "redis_cluster_known_nodes",
			Help: "Number of nodes known to the Redis cluster.",
		},
		[]string{"name", "namespace"},
	)

	// ClusterMasterCount tracks the number of master nodes per cluster.
	ClusterMasterCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "redis_cluster_master_count",
			Help: "Number of master nodes in the Redis cluster.",
		},
		[]string{"name", "namespace"},
	)

	// ClusterReplicaCount tracks the number of replica nodes per cluster.
	ClusterReplicaCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "redis_cluster_replica_count",
			Help: "Number of replica nodes in the Redis cluster.",
		},
		[]string{"name", "namespace"},
	)

	// ClusterScalingOperations counts scaling operations by direction.
	ClusterScalingOperations = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "redis_cluster_scaling_operations_total",
			Help: "Total number of scaling operations performed on Redis clusters.",
		},
		[]string{"name", "namespace", "direction"},
	)

	// ClusterScalingDuration observes how long scaling operations take.
	ClusterScalingDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "redis_cluster_scaling_duration_seconds",
			Help:    "Duration of cluster scaling operations in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"name", "namespace", "direction"},
	)

	// ClusterSlotMigrationTotal counts slot migration operations.
	ClusterSlotMigrationTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "redis_cluster_slot_migration_total",
			Help: "Total number of hash slot migrations performed.",
		},
		[]string{"name", "namespace"},
	)

	// ClusterSlotMigrationDuration observes migration durations.
	ClusterSlotMigrationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "redis_cluster_slot_migration_duration_seconds",
			Help:    "Duration of hash slot migration operations in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"name", "namespace"},
	)

	// ClusterFailoverTotal counts failover events per cluster.
	ClusterFailoverTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "redis_cluster_failover_total",
			Help: "Total number of failover events in Redis clusters.",
		},
		[]string{"name", "namespace"},
	)

	// ClusterFailoverDuration observes failover durations.
	ClusterFailoverDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "redis_cluster_failover_duration_seconds",
			Help:    "Duration of failover operations in seconds.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"name", "namespace"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		ReconcileTotal,
		ReconcileDuration,
		ReconcileErrors,
		ManagedClusters,
		ManagedSentinels,
		ManagedStandalone,
		ClusterState,
		ClusterAssignedSlots,
		ClusterKnownNodes,
		ClusterMasterCount,
		ClusterReplicaCount,
		ClusterScalingOperations,
		ClusterScalingDuration,
		ClusterSlotMigrationTotal,
		ClusterSlotMigrationDuration,
		ClusterFailoverTotal,
		ClusterFailoverDuration,
	)
}
