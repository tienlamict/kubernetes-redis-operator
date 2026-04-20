package resources

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

// BuildSentinelRedisPDB creates a PodDisruptionBudget for RedisSentinel Redis pods.
func BuildSentinelRedisPDB(rs *redisv1alpha1.RedisSentinel) *policyv1.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name + "-redis-pdb",
			Namespace: rs.Namespace,
			Labels:    CommonLabels(rs.Name, ComponentRedis),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rs.Name, ComponentRedis),
			},
		},
	}
}

// BuildSentinelPDB creates a PodDisruptionBudget for Sentinel pods.
func BuildSentinelPDB(rs *redisv1alpha1.RedisSentinel) *policyv1.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name + "-sentinel-pdb",
			Namespace: rs.Namespace,
			Labels:    CommonLabels(rs.Name, ComponentSentinel),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rs.Name, ComponentSentinel),
			},
		},
	}
}

// BuildClusterMasterPDB creates a PodDisruptionBudget for RedisCluster master pods.
func BuildClusterMasterPDB(rc *redisv1alpha1.RedisCluster) *policyv1.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rc.Name + "-masters-pdb",
			Namespace: rc.Namespace,
			Labels:    CommonLabels(rc.Name, ComponentMaster),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rc.Name, ComponentMaster),
			},
		},
	}
}

// BuildClusterReplicaPDB creates a PodDisruptionBudget for RedisCluster replica pods.
func BuildClusterReplicaPDB(rc *redisv1alpha1.RedisCluster) *policyv1.PodDisruptionBudget {
	maxUnavailable := intstr.FromInt(1)
	return &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rc.Name + "-replicas-pdb",
			Namespace: rc.Namespace,
			Labels:    CommonLabels(rc.Name, ComponentReplica),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MaxUnavailable: &maxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rc.Name, ComponentReplica),
			},
		},
	}
}
