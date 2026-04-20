package resources

import (
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

const (
	exporterPortName = "metrics"
	metricsPath      = "/metrics"
	scrapeInterval   = "30s"
)

// BuildRedisServiceMonitor creates a ServiceMonitor for a standalone Redis instance.
func BuildRedisServiceMonitor(redis *redisv1alpha1.Redis) *monitoringv1.ServiceMonitor {
	return buildServiceMonitor(
		redis.Name+"-monitor",
		redis.Namespace,
		CommonLabels(redis.Name, ComponentRedis),
		PodLabels(redis.Name, ComponentRedis),
	)
}

// BuildSentinelServiceMonitor creates a ServiceMonitor for a RedisSentinel instance.
func BuildSentinelServiceMonitor(rs *redisv1alpha1.RedisSentinel) *monitoringv1.ServiceMonitor {
	return buildServiceMonitor(
		rs.Name+"-monitor",
		rs.Namespace,
		CommonLabels(rs.Name, ComponentRedis),
		PodLabels(rs.Name, ComponentRedis),
	)
}

// BuildClusterServiceMonitor creates a ServiceMonitor for a RedisCluster instance.
func BuildClusterServiceMonitor(rc *redisv1alpha1.RedisCluster) *monitoringv1.ServiceMonitor {
	return buildServiceMonitor(
		rc.Name+"-monitor",
		rc.Namespace,
		CommonLabels(rc.Name, ComponentRedis),
		map[string]string{labelInstance: rc.Name},
	)
}

func buildServiceMonitor(name, namespace string, labels, selector map[string]string) *monitoringv1.ServiceMonitor {
	interval := monitoringv1.Duration(scrapeInterval)
	return &monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: monitoringv1.ServiceMonitorSpec{
			Selector: metav1.LabelSelector{
				MatchLabels: selector,
			},
			Endpoints: []monitoringv1.Endpoint{
				{
					Port:     exporterPortName,
					Path:     metricsPath,
					Interval: interval,
				},
			},
		},
	}
}
