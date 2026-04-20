package resources

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

// ConfigMapNameForRedis returns the ConfigMap name for a Redis standalone instance.
func ConfigMapNameForRedis(crName string) string {
	return fmt.Sprintf("%s-config", crName)
}

// ConfigMapNameForSentinel returns the ConfigMap name for a RedisSentinel instance.
func ConfigMapNameForSentinel(crName string) string {
	return fmt.Sprintf("%s-sentinel-config", crName)
}

// BuildRedisConfigMap builds the redis.conf ConfigMap for a standalone Redis instance.
func BuildRedisConfigMap(redis *redisv1alpha1.Redis) *corev1.ConfigMap {
	conf := defaultRedisConf(redis.Spec.RedisConfig, redis.Spec.TLS != nil)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapNameForRedis(redis.Name),
			Namespace: redis.Namespace,
			Labels:    CommonLabels(redis.Name, ComponentRedis),
		},
		Data: map[string]string{
			"redis.conf": conf,
		},
	}
}

// BuildSentinelRedisConfigMap builds the redis.conf ConfigMap for a RedisSentinel instance.
func BuildSentinelRedisConfigMap(rs *redisv1alpha1.RedisSentinel) *corev1.ConfigMap {
	conf := defaultRedisConf(rs.Spec.RedisConfig, rs.Spec.TLS != nil)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapNameForRedis(rs.Name),
			Namespace: rs.Namespace,
			Labels:    CommonLabels(rs.Name, ComponentRedis),
		},
		Data: map[string]string{
			"redis.conf": conf,
		},
	}
}

// BuildSentinelConfigMap builds the sentinel.conf ConfigMap for a RedisSentinel instance.
func BuildSentinelConfigMap(rs *redisv1alpha1.RedisSentinel, masterIP string) *corev1.ConfigMap {
	quorum := int(rs.Spec.SentinelReplicas/2) + 1
	defaults := map[string]string{
		"sentinel monitor mymaster": fmt.Sprintf("%s 6379 %d", masterIP, quorum),
		"sentinel down-after-milliseconds mymaster": "5000",
		"sentinel failover-timeout mymaster":         "10000",
		"sentinel parallel-syncs mymaster":           "1",
	}
	for k, v := range rs.Spec.SentinelConfig {
		defaults[k] = v
	}
	conf := renderConf(defaults)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapNameForSentinel(rs.Name),
			Namespace: rs.Namespace,
			Labels:    CommonLabels(rs.Name, ComponentSentinel),
		},
		Data: map[string]string{
			"sentinel.conf": conf,
		},
	}
}

// BuildClusterConfigMap builds the redis.conf ConfigMap for a RedisCluster instance.
func BuildClusterConfigMap(rc *redisv1alpha1.RedisCluster) *corev1.ConfigMap {
	defaults := map[string]string{
		"bind":                    "0.0.0.0",
		"protected-mode":          "no",
		"port":                    "6379",
		"dir":                     "/data",
		"appendonly":              "yes",
		"save":                    "3600 1 900 10 300 100",
		"cluster-enabled":         "yes",
		"cluster-config-file":     "nodes.conf",
		"cluster-node-timeout":    "15000",
		"cluster-announce-port":   "6379",
		"cluster-announce-bus-port": "16379",
	}
	for k, v := range rc.Spec.RedisConfig {
		defaults[k] = v
	}
	// cluster-announce-ip is injected via Downward API env var at runtime,
	// so it is NOT set statically in the ConfigMap.
	conf := renderConf(defaults)
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapNameForRedis(rc.Name),
			Namespace: rc.Namespace,
			Labels:    CommonLabels(rc.Name, ComponentRedis),
		},
		Data: map[string]string{
			"redis.conf": conf,
		},
	}
}

// defaultRedisConf merges operator defaults with user-provided overrides.
func defaultRedisConf(userConfig map[string]string, tlsEnabled bool) string {
	defaults := map[string]string{
		"bind":           "0.0.0.0",
		"protected-mode": "no",
		"dir":            "/data",
		"appendonly":     "yes",
		"save":           "3600 1 900 10 300 100",
	}
	if tlsEnabled {
		defaults["port"] = "0"
		defaults["tls-port"] = "6379"
		defaults["tls-replication"] = "yes"
		defaults["tls-cluster"] = "yes"
	} else {
		defaults["port"] = "6379"
	}
	for k, v := range userConfig {
		defaults[k] = v
	}
	return renderConf(defaults)
}

// renderConf converts a map into a sorted redis.conf / sentinel.conf string.
func renderConf(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(fmt.Sprintf("%s %s\n", k, m[k]))
	}
	return sb.String()
}
