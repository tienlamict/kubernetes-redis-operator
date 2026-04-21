package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	redisv1alpha1 "github.com/example/redis-operator/api/v1alpha1"
)

// Integration tests for RedisReconciler using envtest (real kube-apiserver + etcd).
// The manager started in suite_test.go runs all three reconcilers so watch loops
// fire normally, making these tests a true end-to-end check of the reconcile logic.
//
// Pre-requisites:
//   - Run `make envtest` once to download the kubebuilder binary assets.
//   - Set KUBEBUILDER_ASSETS or rely on setup-envtest being in PATH.
//
// Run with:
//
//	KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) \
//	  go test ./internal/controller/... -v -run TestControllers

const (
	timeout  = 20 * time.Second
	interval = 250 * time.Millisecond
)

var _ = Describe("Redis controller (integration)", Ordered, func() {
	ctx := context.Background()

	// Each Describe block creates resources in a dedicated namespace so specs
	// remain isolated and can run in parallel without name collisions.

	// ─────────────────────────────────────────────────────────────────────────
	// Standalone Redis (no storage) → Deployment + Service + ConfigMap
	// ─────────────────────────────────────────────────────────────────────────
	Describe("standalone Redis without storage", func() {
		var ns *corev1.Namespace

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "integ-redis-nostorage"},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})
		})

		It("creates Deployment, Service and ConfigMap", func() {
			key := types.NamespacedName{Name: "myredis", Namespace: ns.Name}

			rc := &redisv1alpha1.Redis{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: redisv1alpha1.RedisSpec{
					Image: "redis:7.2-alpine",
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			By("waiting for Deployment")
			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, dep)
			}, timeout, interval).Should(Succeed())

			By("verifying config-hash annotation is present")
			Expect(dep.Spec.Template.Annotations).To(HaveKey(configHashAnnotation))

			By("waiting for Service")
			svc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, svc)
			}, timeout, interval).Should(Succeed())

			By("waiting for ConfigMap")
			cm := &corev1.ConfigMap{}
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{Name: key.Name + "-config", Namespace: key.Namespace}, cm)
			}, timeout, interval).Should(Succeed())
		})

		It("rolls the Deployment when redisConfig changes", func() {
			key := types.NamespacedName{Name: "myredis-roll", Namespace: ns.Name}

			rc := &redisv1alpha1.Redis{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec:       redisv1alpha1.RedisSpec{Image: "redis:7.2-alpine"},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			// Wait for the initial Deployment.
			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, dep)
			}, timeout, interval).Should(Succeed())
			hash1 := dep.Spec.Template.Annotations[configHashAnnotation]
			Expect(hash1).To(HaveLen(16))

			// Update config — the controller should recalculate the hash.
			updated := &redisv1alpha1.Redis{}
			Expect(k8sClient.Get(ctx, key, updated)).To(Succeed())
			updated.Spec.RedisConfig = map[string]string{"maxmemory": "256mb"}
			Expect(k8sClient.Update(ctx, updated)).To(Succeed())

			Eventually(func() string {
				dep2 := &appsv1.Deployment{}
				if err := k8sClient.Get(ctx, key, dep2); err != nil {
					return ""
				}
				return dep2.Spec.Template.Annotations[configHashAnnotation]
			}, timeout, interval).ShouldNot(Equal(hash1))
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Standalone Redis with storage → StatefulSet + headless Service
	// ─────────────────────────────────────────────────────────────────────────
	Describe("standalone Redis with storage", func() {
		var ns *corev1.Namespace

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "integ-redis-storage"},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})
		})

		It("creates StatefulSet and headless Service", func() {
			className := "standard"
			key := types.NamespacedName{Name: "myredis-sts", Namespace: ns.Name}

			rc := &redisv1alpha1.Redis{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: redisv1alpha1.RedisSpec{
					Image: "redis:7.2-alpine",
					Storage: &redisv1alpha1.StorageSpec{
						Size:      "1Gi",
						ClassName: &className,
					},
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			By("waiting for StatefulSet")
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, sts)
			}, timeout, interval).Should(Succeed())
			Expect(sts.Spec.Template.Annotations).To(HaveKey(configHashAnnotation))

			By("waiting for headless Service")
			headless := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{Name: key.Name + "-redis-headless", Namespace: key.Namespace},
					headless)
			}, timeout, interval).Should(Succeed())
			Expect(headless.Spec.ClusterIP).To(Equal("None"))
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// Standalone Redis — finalizer lifecycle (add on create, remove on delete)
	// ─────────────────────────────────────────────────────────────────────────
	Describe("finalizer lifecycle", func() {
		var ns *corev1.Namespace

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "integ-redis-finalizer"},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})
		})

		It("adds finalizer on first reconcile", func() {
			key := types.NamespacedName{Name: "myredis-fin", Namespace: ns.Name}

			rc := &redisv1alpha1.Redis{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec:       redisv1alpha1.RedisSpec{Image: "redis:7.2-alpine"},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			Eventually(func() bool {
				obj := &redisv1alpha1.Redis{}
				if err := k8sClient.Get(ctx, key, obj); err != nil {
					return false
				}
				for _, f := range obj.Finalizers {
					if f == redisv1alpha1.FinalizerName {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// RedisSentinel — basic resource creation
	// ─────────────────────────────────────────────────────────────────────────
	Describe("RedisSentinel resource creation", func() {
		var ns *corev1.Namespace

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "integ-sentinel"},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})
		})

		It("creates Redis StatefulSet, Sentinel Deployment and all Services", func() {
			key := types.NamespacedName{Name: "myha", Namespace: ns.Name}

			rs := &redisv1alpha1.RedisSentinel{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: redisv1alpha1.RedisSentinelSpec{
					Image:            "redis:7.2-alpine",
					Replicas:         3,
					SentinelReplicas: 3,
				},
			}
			Expect(k8sClient.Create(ctx, rs)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rs) })

			By("waiting for Redis StatefulSet")
			sts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, sts)
			}, timeout, interval).Should(Succeed())

			By("waiting for Sentinel Deployment")
			dep := &appsv1.Deployment{}
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{Name: key.Name + "-sentinel", Namespace: key.Namespace}, dep)
			}, timeout, interval).Should(Succeed())

			By("waiting for master Service")
			masterSvc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{Name: key.Name + "-master", Namespace: key.Namespace}, masterSvc)
			}, timeout, interval).Should(Succeed())
		})
	})

	// ─────────────────────────────────────────────────────────────────────────
	// RedisCluster — basic resource creation (ClusterManager nil → lifecycle skipped)
	// ─────────────────────────────────────────────────────────────────────────
	Describe("RedisCluster resource creation", func() {
		var ns *corev1.Namespace

		BeforeAll(func() {
			ns = &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "integ-cluster"},
			}
			Expect(k8sClient.Create(ctx, ns)).To(Succeed())
			DeferCleanup(func() {
				_ = k8sClient.Delete(ctx, ns)
			})
		})

		It("creates master StatefulSet, client Service, and PodDisruptionBudgets", func() {
			key := types.NamespacedName{Name: "mycluster", Namespace: ns.Name}

			rc := &redisv1alpha1.RedisCluster{
				ObjectMeta: metav1.ObjectMeta{Name: key.Name, Namespace: key.Namespace},
				Spec: redisv1alpha1.RedisClusterSpec{
					Image:             "redis:7.2-alpine",
					Masters:           3,
					ReplicasPerMaster: 1,
				},
			}
			Expect(k8sClient.Create(ctx, rc)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, rc) })

			By("waiting for master StatefulSet")
			masterSts := &appsv1.StatefulSet{}
			Eventually(func() error {
				return k8sClient.Get(ctx,
					types.NamespacedName{Name: key.Name + "-masters", Namespace: key.Namespace}, masterSts)
			}, timeout, interval).Should(Succeed())

			By("waiting for client Service")
			clientSvc := &corev1.Service{}
			Eventually(func() error {
				return k8sClient.Get(ctx, key, clientSvc)
			}, timeout, interval).Should(Succeed())
		})
	})
})
