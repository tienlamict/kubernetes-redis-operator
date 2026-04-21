package resources

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

const (
	RedisPort        = 6379
	SentinelPort     = 26379
	RedisClusterBus  = 16379
	ExporterPort     = 9121
)

// BuildRedisService creates the ClusterIP Service for a standalone Redis instance.
func BuildRedisService(redis *redisv1alpha1.Redis) *corev1.Service {
	labels := CommonLabels(redis.Name, ComponentRedis)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redis.Name,
			Namespace: redis.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: PodLabels(redis.Name, ComponentRedis),
			Ports: []corev1.ServicePort{
				{
					Name:       "redis",
					Port:       RedisPort,
					TargetPort: intstr.FromInt(RedisPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildSentinelMasterService creates the ClusterIP Service that always points to the current master.
// The selector uses the RoleLabelKey label which the controller updates on each reconcile after
// querying Sentinel for the current master pod.
func BuildSentinelMasterService(rs *redisv1alpha1.RedisSentinel) *corev1.Service {
	labels := CommonLabels(rs.Name, ComponentMaster)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name + "-master",
			Namespace: rs.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				labelInstance: rs.Name,
				RoleLabelKey:  RoleMaster,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "redis",
					Port:       RedisPort,
					TargetPort: intstr.FromInt(RedisPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildSentinelReplicaService creates the ClusterIP Service pointing to all Redis replicas.
// The selector uses the RoleLabelKey label which the controller updates on each reconcile.
func BuildSentinelReplicaService(rs *redisv1alpha1.RedisSentinel) *corev1.Service {
	labels := CommonLabels(rs.Name, ComponentReplica)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name + "-replica",
			Namespace: rs.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Selector: map[string]string{
				labelInstance: rs.Name,
				RoleLabelKey:  RoleReplica,
			},
			Ports: []corev1.ServicePort{
				{
					Name:       "redis",
					Port:       RedisPort,
					TargetPort: intstr.FromInt(RedisPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildSentinelService creates the ClusterIP Service for Sentinel instances.
func BuildSentinelService(rs *redisv1alpha1.RedisSentinel) *corev1.Service {
	labels := CommonLabels(rs.Name, ComponentSentinel)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name + "-sentinel",
			Namespace: rs.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: PodLabels(rs.Name, ComponentSentinel),
			Ports: []corev1.ServicePort{
				{
					Name:       "sentinel",
					Port:       SentinelPort,
					TargetPort: intstr.FromInt(SentinelPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// BuildHeadlessService creates a headless Service for StatefulSet stable DNS.
func BuildHeadlessService(name, namespace, component string) *corev1.Service {
	labels := CommonLabels(name, component)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      HeadlessServiceName(name, component),
			Namespace: namespace,
			Labels:    labels,
			Annotations: map[string]string{
				"service.alpha.kubernetes.io/tolerate-unready-endpoints": "true",
			},
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: "None",
			Selector:  PodLabels(name, component),
			Ports: []corev1.ServicePort{
				{
					Name:       "redis",
					Port:       RedisPort,
					TargetPort: intstr.FromInt(RedisPort),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       "cluster-bus",
					Port:       RedisClusterBus,
					TargetPort: intstr.FromInt(RedisClusterBus),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			PublishNotReadyAddresses: true,
		},
	}
}

// BuildClusterClientService creates the ClusterIP Service for client access to a RedisCluster.
func BuildClusterClientService(rc *redisv1alpha1.RedisCluster) *corev1.Service {
	labels := CommonLabels(rc.Name, ComponentRedis)
	// Selector matches all pods (masters + replicas) by instance name.
	selector := map[string]string{
		labelInstance: rc.Name,
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rc.Name,
			Namespace: rc.Namespace,
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: selector,
			Ports: []corev1.ServicePort{
				{
					Name:       "redis",
					Port:       RedisPort,
					TargetPort: intstr.FromInt(RedisPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}
