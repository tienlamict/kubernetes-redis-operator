package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

// BuildRedisStatefulSet creates the StatefulSet for a standalone Redis instance (with storage).
func BuildRedisStatefulSet(redis *redisv1alpha1.Redis) *appsv1.StatefulSet {
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
		true,
	)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      redis.Name,
			Namespace: redis.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: HeadlessServiceName(redis.Name, ComponentRedis),
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

	if redis.Spec.Storage != nil {
		sts.Spec.VolumeClaimTemplates = buildPVCTemplates(redis.Spec.Storage)
	}
	return sts
}

// BuildSentinelRedisStatefulSet creates the StatefulSet for RedisSentinel Redis pods.
func BuildSentinelRedisStatefulSet(rs *redisv1alpha1.RedisSentinel) *appsv1.StatefulSet {
	labels := CommonLabels(rs.Name, ComponentRedis)
	replicas := rs.Spec.Replicas
	podSpec := buildRedisPodSpec(
		rs.Spec.Image,
		rs.Spec.ExporterImage,
		rs.Spec.EnableExporter,
		rs.Spec.Resources,
		rs.Spec.Auth,
		rs.Spec.TLS,
		rs.Spec.Affinity,
		rs.Spec.Tolerations,
		rs.Spec.NodeSelector,
		ConfigMapNameForRedis(rs.Name),
		false,
	)

	// Add init container that configures replication on startup.
	podSpec.InitContainers = []corev1.Container{
		buildSentinelInitContainer(rs),
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs.Name,
			Namespace: rs.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: HeadlessServiceName(rs.Name, ComponentRedis),
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rs.Name, ComponentRedis),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: PodLabels(rs.Name, ComponentRedis),
				},
				Spec: podSpec,
			},
		},
	}

	if rs.Spec.Storage != nil {
		sts.Spec.VolumeClaimTemplates = buildPVCTemplates(rs.Spec.Storage)
	}
	return sts
}

// BuildClusterMasterStatefulSet creates the StatefulSet for RedisCluster master pods.
func BuildClusterMasterStatefulSet(rc *redisv1alpha1.RedisCluster) *appsv1.StatefulSet {
	labels := CommonLabels(rc.Name, ComponentMaster)
	replicas := rc.Spec.Masters

	resources := rc.Spec.Resources
	if rc.Spec.MasterResources != nil {
		resources = *rc.Spec.MasterResources
	}

	podSpec := buildClusterPodSpec(
		rc.Spec.Image,
		rc.Spec.ExporterImage,
		rc.Spec.EnableExporter,
		resources,
		rc.Spec.Auth,
		rc.Spec.TLS,
		rc.Spec.MasterAffinity,
		rc.Spec.MasterTolerations,
		rc.Spec.MasterNodeSelector,
		ConfigMapNameForRedis(rc.Name),
	)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rc.Name + "-masters",
			Namespace: rc.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: HeadlessServiceName(rc.Name, ComponentMaster),
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rc.Name, ComponentMaster),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: PodLabels(rc.Name, ComponentMaster),
				},
				Spec: podSpec,
			},
		},
	}

	if rc.Spec.Storage != nil {
		sts.Spec.VolumeClaimTemplates = buildPVCTemplates(rc.Spec.Storage)
	}
	return sts
}

// BuildClusterReplicaStatefulSet creates the StatefulSet for RedisCluster replica pods.
func BuildClusterReplicaStatefulSet(rc *redisv1alpha1.RedisCluster) *appsv1.StatefulSet {
	labels := CommonLabels(rc.Name, ComponentReplica)
	replicas := rc.Spec.Masters * rc.Spec.ReplicasPerMaster

	resources := rc.Spec.Resources
	if rc.Spec.ReplicaResources != nil {
		resources = *rc.Spec.ReplicaResources
	}

	podSpec := buildClusterPodSpec(
		rc.Spec.Image,
		rc.Spec.ExporterImage,
		rc.Spec.EnableExporter,
		resources,
		rc.Spec.Auth,
		rc.Spec.TLS,
		rc.Spec.ReplicaAffinity,
		rc.Spec.ReplicaTolerations,
		rc.Spec.ReplicaNodeSelector,
		ConfigMapNameForRedis(rc.Name),
	)

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rc.Name + "-replicas",
			Namespace: rc.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    &replicas,
			ServiceName: HeadlessServiceName(rc.Name, ComponentReplica),
			Selector: &metav1.LabelSelector{
				MatchLabels: PodLabels(rc.Name, ComponentReplica),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: PodLabels(rc.Name, ComponentReplica),
				},
				Spec: podSpec,
			},
		},
	}

	if rc.Spec.Storage != nil {
		sts.Spec.VolumeClaimTemplates = buildPVCTemplates(rc.Spec.Storage)
	}
	return sts
}

// buildRedisPodSpec constructs a PodSpec for a standalone or sentinel Redis pod.
func buildRedisPodSpec(
	image, exporterImage string,
	enableExporter *bool,
	resources corev1.ResourceRequirements,
	auth *redisv1alpha1.AuthSpec,
	tls *redisv1alpha1.TLSSpec,
	affinity *corev1.Affinity,
	tolerations []corev1.Toleration,
	nodeSelector map[string]string,
	configMapName string,
	hasStorage bool,
) corev1.PodSpec {
	containers := []corev1.Container{
		buildRedisContainer(image, resources, auth, tls, configMapName, hasStorage),
	}
	if enableExporter == nil || *enableExporter {
		containers = append(containers, buildExporterContainer(exporterImage, auth))
	}

	spec := corev1.PodSpec{
		Containers:   containers,
		Volumes:      buildVolumes(configMapName, tls),
		Affinity:     affinity,
		Tolerations:  tolerations,
		NodeSelector: nodeSelector,
	}
	return spec
}

// buildClusterPodSpec constructs a PodSpec for a cluster node.
func buildClusterPodSpec(
	image, exporterImage string,
	enableExporter *bool,
	resources corev1.ResourceRequirements,
	auth *redisv1alpha1.AuthSpec,
	tls *redisv1alpha1.TLSSpec,
	affinity *corev1.Affinity,
	tolerations []corev1.Toleration,
	nodeSelector map[string]string,
	configMapName string,
) corev1.PodSpec {
	redisContainer := buildRedisContainer(image, resources, auth, tls, configMapName, true)
	// Inject cluster-announce-ip via Downward API.
	redisContainer.Env = append(redisContainer.Env, corev1.EnvVar{
		Name: "POD_IP",
		ValueFrom: &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"},
		},
	})
	// Append cluster-announce-ip to the config at startup.
	redisContainer.Command = []string{
		"sh", "-c",
		`echo "cluster-announce-ip ${POD_IP}" >> /etc/redis/redis.conf && exec redis-server /etc/redis/redis.conf`,
	}

	containers := []corev1.Container{redisContainer}
	if enableExporter == nil || *enableExporter {
		containers = append(containers, buildExporterContainer(exporterImage, auth))
	}
	return corev1.PodSpec{
		Containers:   containers,
		Volumes:      buildVolumes(configMapName, tls),
		Affinity:     affinity,
		Tolerations:  tolerations,
		NodeSelector: nodeSelector,
	}
}

func buildRedisContainer(
	image string,
	resources corev1.ResourceRequirements,
	auth *redisv1alpha1.AuthSpec,
	tls *redisv1alpha1.TLSSpec,
	configMapName string,
	hasStorage bool,
) corev1.Container {
	if image == "" {
		image = redisv1alpha1.DefaultRedisImage
	}
	mounts := []corev1.VolumeMount{
		{Name: "config", MountPath: "/etc/redis"},
	}
	if hasStorage {
		mounts = append(mounts, corev1.VolumeMount{Name: "data", MountPath: "/data"})
	}
	if tls != nil {
		mounts = append(mounts, corev1.VolumeMount{Name: "tls", MountPath: "/tls", ReadOnly: true})
	}

	env := []corev1.EnvVar{}
	if auth != nil {
		env = append(env, corev1.EnvVar{
			Name: "REDISCLI_AUTH",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: auth.SecretName},
					Key:                  "password",
				},
			},
		})
	}

	probe := &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{
				Command: []string{"redis-cli", "PING"},
			},
		},
		InitialDelaySeconds: 10,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		FailureThreshold:    3,
	}
	readinessProbe := probe.DeepCopy()
	readinessProbe.PeriodSeconds = 5
	readinessProbe.TimeoutSeconds = 3
	readinessProbe.InitialDelaySeconds = 5

	c := corev1.Container{
		Name:            "redis",
		Image:           image,
		Command:         []string{"redis-server", "/etc/redis/redis.conf"},
		Resources:       resources,
		Env:             env,
		VolumeMounts:    mounts,
		LivenessProbe:   probe,
		ReadinessProbe:  readinessProbe,
		Ports: []corev1.ContainerPort{
			{Name: "redis", ContainerPort: RedisPort, Protocol: corev1.ProtocolTCP},
		},
		Lifecycle: &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/scripts/redis-shutdown.sh"},
				},
			},
		},
	}
	_ = configMapName // used by the volume definition, not the container directly
	return c
}

func buildExporterContainer(exporterImage string, auth *redisv1alpha1.AuthSpec) corev1.Container {
	if exporterImage == "" {
		exporterImage = redisv1alpha1.DefaultExporterImage
	}
	env := []corev1.EnvVar{
		{Name: "REDIS_ADDR", Value: fmt.Sprintf("redis://localhost:%d", RedisPort)},
	}
	if auth != nil {
		env = append(env, corev1.EnvVar{
			Name: "REDIS_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: auth.SecretName},
					Key:                  "password",
				},
			},
		})
	}
	return corev1.Container{
		Name:      "redis-exporter",
		Image:     exporterImage,
		Env:       env,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		Ports: []corev1.ContainerPort{
			{Name: "metrics", ContainerPort: ExporterPort, Protocol: corev1.ProtocolTCP},
		},
	}
}

func buildVolumes(configMapName string, tls *redisv1alpha1.TLSSpec) []corev1.Volume {
	volumes := []corev1.Volume{
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			},
		},
	}
	if tls != nil {
		volumes = append(volumes, corev1.Volume{
			Name: "tls",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: tls.SecretName},
			},
		})
	}
	return volumes
}

func buildPVCTemplates(storage *redisv1alpha1.StorageSpec) []corev1.PersistentVolumeClaim {
	storageSize, _ := resource.ParseQuantity(storage.Size)
	pvc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageSize,
				},
			},
		},
	}
	if storage.ClassName != nil {
		pvc.Spec.StorageClassName = storage.ClassName
	}
	return []corev1.PersistentVolumeClaim{pvc}
}

// buildSentinelInitContainer creates the init container that configures master/replica roles.
func buildSentinelInitContainer(rs *redisv1alpha1.RedisSentinel) corev1.Container {
	headlessSvc := HeadlessServiceName(rs.Name, ComponentRedis)
	// Pod index 0 is always the initial master.
	script := fmt.Sprintf(`
set -e
INDEX=$(echo $POD_NAME | rev | cut -d- -f1 | rev)
if [ "$INDEX" = "0" ]; then
  echo "This is the master pod, no REPLICAOF needed."
else
  MASTER="%s-0.%s.%s.svc.cluster.local"
  echo "Configuring REPLICAOF $MASTER 6379"
  echo "replicaof $MASTER 6379" >> /etc/redis/redis.conf
fi
`, rs.Name, headlessSvc, rs.Namespace)

	return corev1.Container{
		Name:    "init-replication",
		Image:   rs.Spec.Image,
		Command: []string{"sh", "-c", script},
		Env: []corev1.EnvVar{
			{
				Name: "POD_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "config", MountPath: "/etc/redis"},
		},
	}
}
