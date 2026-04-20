package resources

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

// BuildRedisDeployment creates a Deployment for a standalone Redis instance without persistent storage.
func BuildRedisDeployment(redis *redisv1alpha1.Redis) *appsv1.Deployment {
	labels := CommonLabels(redis.Name, ComponentRedis)
	replicas := int32(1)
	podSpec := buildRedisPodSpec(
		redis.Spec.Image,
		redis.Spec.ExporterImage,
		redis.Spec.EnableExporter,
		redis.Spec.Resources,
		redis.Spec.Auth,
		redis.Spec.TLS,
		redis.Spec.Affinity,
		redis.Spec.Tolerations,
		redis.Spec.NodeSelector,
		ConfigMapNameForRedis(redis.Name),
		false,
	)
	// Add emptyDir for /data when no PVC is used.
	podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
		Name:         "data",
		VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
	})
	// Ensure the Redis container mounts /data.
	for i := range podSpec.Containers {
		if podSpec.Containers[i].Name == "redis" {
			podSpec.Containers[i].VolumeMounts = append(podSpec.Containers[i].VolumeMounts,
				corev1.VolumeMount{Name: "data", MountPath: "/data"})
		}
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redis.Name,
			Namespace: redis.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(redis.Name, ComponentRedis),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: PodLabels(redis.Name, ComponentRedis),
				},
				Spec: podSpec,
			},
		},
	}
}

// BuildSentinelDeployment creates a Deployment for Sentinel pods.
func BuildSentinelDeployment(rs *redisv1alpha1.RedisSentinel) *appsv1.Deployment {
	labels := CommonLabels(rs.Name, ComponentSentinel)
	replicas := rs.Spec.SentinelReplicas

	sentinelImage := rs.Spec.SentinelImage
	if sentinelImage == "" {
		sentinelImage = rs.Spec.Image
	}
	if sentinelImage == "" {
		sentinelImage = redisv1alpha1.DefaultRedisImage
	}

	env := []corev1.EnvVar{}
	if rs.Spec.Auth != nil {
		env = append(env, corev1.EnvVar{
			Name: "REDISCLI_AUTH",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: rs.Spec.Auth.SecretName},
					Key:                  "password",
				},
			},
		})
	}

	volumes := []corev1.Volume{
		{
			Name: "sentinel-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: ConfigMapNameForSentinel(rs.Name)},
				},
			},
		},
		{
			Name:         "sentinel-data",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	}

	sentinelProbe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"redis-cli", "-p", "26379", "PING"},
			},
		},
		InitialDelaySeconds: 15,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
	}

	sentinelContainer := corev1.Container{
		Name:  "sentinel",
		Image: sentinelImage,
		Command: []string{
			"redis-server",
			"/etc/sentinel/sentinel.conf",
			"--sentinel",
		},
		Env: env,
		Ports: []corev1.ContainerPort{
			{Name: "sentinel", ContainerPort: SentinelPort, Protocol: corev1.ProtocolTCP},
		},
		Resources: rs.Spec.SentinelResources,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "sentinel-config", MountPath: "/etc/sentinel"},
			{Name: "sentinel-data", MountPath: "/data"},
		},
		LivenessProbe:  sentinelProbe,
		ReadinessProbe: sentinelProbe.DeepCopy(),
	}

	podSpec := corev1.PodSpec{
		Containers: []corev1.Container{sentinelContainer},
		Volumes:    volumes,
	}

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name + "-sentinel",
			Namespace: rs.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rs.Name, ComponentSentinel),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: PodLabels(rs.Name, ComponentSentinel),
				},
				Spec: podSpec,
			},
		},
	}
}

