# Kubernetes Redis Operator

A production-grade Kubernetes operator for managing Redis workloads, built with
[Kubebuilder v4](https://book.kubebuilder.io/) and
[controller-runtime v0.19](https://pkg.go.dev/sigs.k8s.io/controller-runtime).

The operator provides three custom resources that cover the full spectrum of Redis
deployment patterns — from a single-node cache to a fully sharded cluster.

---

## Table of Contents

- [Features](#features)
- [Architecture](#architecture)
- [Prerequisites](#prerequisites)
- [Quick Start](#quick-start)
- [Custom Resources](#custom-resources)
  - [Redis (Standalone)](#redis-standalone)
  - [RedisSentinel (High-Availability)](#redissentinel-high-availability)
  - [RedisCluster (Sharded)](#rediscluster-sharded)
- [Observability](#observability)
- [Development](#development)
- [Project Structure](#project-structure)

---

## Features

| Feature | Redis | RedisSentinel | RedisCluster |
|---------|-------|---------------|--------------|
| Deployment (no storage) | ✅ | — | — |
| StatefulSet + PVC | ✅ | ✅ | ✅ |
| Headless Service | — | ✅ | ✅ |
| Config rolling restart | ✅ | ✅ | ✅ |
| Prometheus exporter sidecar | ✅ | ✅ | ✅ |
| Auth (Secret reference) | ✅ | ✅ | ✅ |
| TLS termination | ✅ | ✅ | ✅ |
| Webhook validation | ✅ | ✅ | ✅ |
| PodDisruptionBudget | — | — | ✅ |
| Scale-out / scale-in | — | — | ✅ |
| Automatic failover detection | — | ✅ | ✅ |
| Finalizer / safe deletion | ✅ | ✅ | ✅ |
| Operator metrics (Prometheus) | ✅ | ✅ | ✅ |

---

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    Kubernetes API Server                      │
│                                                               │
│   redis.example.com/v1alpha1                                  │
│   ┌──────────┐  ┌────────────────┐  ┌─────────────────────┐  │
│   │  Redis   │  │ RedisSentinel  │  │    RedisCluster     │  │
│   └────┬─────┘  └───────┬────────┘  └──────────┬──────────┘  │
└────────┼────────────────┼──────────────────────┼─────────────┘
         │                │                      │
         ▼                ▼                      ▼
┌─────────────────────────────────────────────────────────────┐
│                     Operator (this repo)                      │
│                                                               │
│  RedisReconciler   RedisSentinelReconciler  RedisCluster      │
│        │                   │               Reconciler         │
│        └───────────────────┴──────────┬────────────          │
│                                       │                       │
│            internal/resources         │  internal/metrics     │
│            ┌──────────────────┐       │  ┌─────────────────┐  │
│            │  Builder helpers │       └─▶│ Prometheus gauges│  │
│            │  (STS/Dep/Svc/   │          │ & counters       │  │
│            │   CM/PDB)        │          └─────────────────┘  │
│            └──────────────────┘                               │
└─────────────────────────────────────────────────────────────┘
         │
         ▼  Creates / updates / deletes
┌────────────────────────────────────────────────┐
│              Kubernetes Resources               │
│  Deployment / StatefulSet / Service / ConfigMap │
│  PVC / PodDisruptionBudget / Events             │
└────────────────────────────────────────────────┘
```

### Config-hash rolling restart

Every reconcile loop computes a SHA-256 hash of the sorted `redis.conf` key-value
pairs and stamps it as the annotation `redis.example.com/config-hash` on the pod
template. Any change to `spec.redisConfig` therefore causes a rolling restart
automatically — no manual `rollout restart` required.

---

## Prerequisites

| Tool | Version |
|------|---------|
| Go | 1.22+ |
| Kubernetes | 1.29+ |
| kubectl | matching cluster |
| controller-gen | v0.16.4 (auto-downloaded by `make`) |
| setup-envtest | release-0.19 (for tests) |

---

## Quick Start

### 1 — Install CRDs

```bash
# Generate latest manifests, then apply
make manifests
kubectl apply -f config/crd/bases/
```

### 2 — Run the operator locally (out-of-cluster)

```bash
make run
```

### 3 — Deploy to a cluster

```bash
# Build and push the image
make docker-build docker-push IMG=your-registry/redis-operator:latest

# Deploy (kustomize)
make deploy IMG=your-registry/redis-operator:latest
```

### 4 — Create your first Redis instance

```bash
kubectl apply -f - <<'EOF'
apiVersion: redis.example.com/v1alpha1
kind: Redis
metadata:
  name: my-redis
  namespace: default
spec:
  image: redis:7.2-alpine
  redisConfig:
    maxmemory: "256mb"
    maxmemory-policy: "allkeys-lru"
EOF

kubectl get redis my-redis -o wide
```

---

## Custom Resources

### Redis (Standalone)

Manages a single Redis instance. When `spec.storage` is omitted the operator
creates a `Deployment`; when storage is set it creates a `StatefulSet` with a
`PersistentVolumeClaim`.

**Minimal example (ephemeral cache)**

```yaml
apiVersion: redis.example.com/v1alpha1
kind: Redis
metadata:
  name: cache
spec:
  image: redis:7.2-alpine
```

**With persistent storage**

```yaml
apiVersion: redis.example.com/v1alpha1
kind: Redis
metadata:
  name: redis-persistent
spec:
  image: redis:7.2-alpine
  storage:
    size: "10Gi"
    className: "standard"
  redisConfig:
    save: "3600 1 300 100 60 10000"
    appendonly: "yes"
```

**Resources created**

| Resource | Name |
|----------|------|
| Deployment or StatefulSet | `<name>` |
| Service (ClusterIP) | `<name>` |
| ConfigMap | `<name>-config` |
| Headless Service *(storage only)* | `<name>-headless` |

---

### RedisSentinel (High-Availability)

Deploys a Redis primary/replica StatefulSet alongside a Sentinel `Deployment`
for automatic failover. At least 3 Sentinel replicas are required for quorum.

```yaml
apiVersion: redis.example.com/v1alpha1
kind: RedisSentinel
metadata:
  name: redis-ha
spec:
  image: redis:7.2-alpine
  replicas: 3          # 1 master + 2 replicas
  sentinelReplicas: 3
  storage:
    size: "20Gi"
  redisConfig:
    maxmemory: "1gb"
```

**Resources created**

| Resource | Name |
|----------|------|
| StatefulSet (Redis pods) | `<name>` |
| Deployment (Sentinel pods) | `<name>-sentinel` |
| Service — master | `<name>-master` |
| Service — replica | `<name>-replica` |
| Headless Service | `<name>-redis-headless` |
| ConfigMap (redis.conf) | `<name>-config` |
| ConfigMap (sentinel.conf) | `<name>-sentinel-config` |

---

### RedisCluster (Sharded)

Deploys a native Redis Cluster using hash-slot sharding. Masters and replicas
are managed as separate StatefulSets. The operator handles slot rebalancing
during scale-out / scale-in via the `ClusterManager` interface (backed by
`go-redis`).

```yaml
apiVersion: redis.example.com/v1alpha1
kind: RedisCluster
metadata:
  name: redis-cluster
spec:
  image: redis:7.2-alpine
  masters: 3
  replicasPerMaster: 1
  storage:
    size: "50Gi"
  redisConfig:
    cluster-node-timeout: "5000"
    maxmemory: "4gb"
    maxmemory-policy: "noeviction"
```

**Resources created**

| Resource | Name |
|----------|------|
| StatefulSet (masters) | `<name>-masters` |
| StatefulSet (replicas) | `<name>-replicas` |
| Service (client) | `<name>` |
| Headless Service (masters) | `<name>-masters-headless` |
| Headless Service (replicas) | `<name>-replicas-headless` |
| ConfigMap | `<name>-config` |
| PodDisruptionBudget (masters) | `<name>-masters-pdb` |
| PodDisruptionBudget (replicas) | `<name>-replicas-pdb` |

**Cluster phases**

```
Initializing → Bootstrapping → Running ↔ ScalingOut / ScalingIn
                                       ↕
                                   Recovering
```

---

## Observability

### Operator metrics

The operator exposes Prometheus metrics on `:8080/metrics` (default
controller-runtime metrics endpoint).

| Metric | Type | Labels |
|--------|------|--------|
| `redis_operator_reconcile_total` | Counter | `kind`, `result` |
| `redis_operator_reconcile_duration_seconds` | Histogram | `kind` |
| `redis_operator_reconcile_errors_total` | Counter | `kind`, `reason` |
| `redis_operator_cluster_state` | Gauge | `name`, `namespace` |
| `redis_operator_cluster_assigned_slots` | Gauge | `name`, `namespace` |
| `redis_operator_cluster_known_nodes` | Gauge | `name`, `namespace` |
| `redis_operator_cluster_master_count` | Gauge | `name`, `namespace` |
| `redis_operator_cluster_replica_count` | Gauge | `name`, `namespace` |
| `redis_operator_cluster_scaling_operations_total` | Counter | `name`, `namespace`, `direction` |
| `redis_operator_cluster_scaling_duration_seconds` | Histogram | `name`, `namespace`, `direction` |
| `redis_operator_cluster_failover_total` | Counter | `name`, `namespace` |

### Redis instance metrics

When `spec.enableExporter: true` (the default), the operator injects an
[oliver006/redis_exporter](https://github.com/oliver006/redis_exporter) sidecar
into every Redis pod, enabling per-instance Prometheus scraping.

---

## Development

### Install toolchain

```bash
make controller-gen   # downloads controller-gen to ./bin/
make envtest          # downloads setup-envtest + kubebuilder binaries
```

### Generate manifests & deepcopy

```bash
make manifests   # regenerates CRDs, RBAC roles, webhook manifests
make generate    # regenerates zz_generated.deepcopy.go
```

### Run unit + integration tests

```bash
# Unit tests only (fake client, no cluster needed)
go test ./... -run "^Test[^C]" -v

# Integration tests (envtest — real kube-apiserver + etcd)
KUBEBUILDER_ASSETS=$(setup-envtest use 1.31.0 -p path) \
  go test ./internal/controller/... -v -run TestControllers

# All tests via make (downloads assets automatically)
make test
```

### Linting

```bash
make lint        # runs golangci-lint
make lint-fix    # auto-fix where possible
```

### Build the binary

```bash
make build       # outputs to ./bin/manager
```

---

## Project Structure

```
.
├── api/v1alpha1/               # CRD types, defaulting & validation webhooks
│   ├── redis_types.go
│   ├── redissentinel_types.go
│   ├── rediscluster_types.go
│   └── *_webhook.go
├── cmd/main.go                 # Operator entry point; registers all controllers
├── config/
│   ├── crd/bases/              # Generated CRD YAML (apply to cluster)
│   └── rbac/                   # Generated RBAC role manifests
├── internal/
│   ├── controller/             # Reconcilers (Redis, RedisSentinel, RedisCluster)
│   │   ├── redis_controller.go
│   │   ├── redissentinel_controller.go
│   │   ├── rediscluster_controller.go
│   │   ├── *_test.go           # Unit tests (fake client)
│   │   ├── redis_integration_test.go   # Envtest integration tests
│   │   └── suite_test.go       # Ginkgo/envtest bootstrap
│   ├── metrics/
│   │   ├── metrics.go          # Prometheus metric declarations
│   │   └── recorder.go         # Helper functions: RecordReconcile, SetClusterMetrics…
│   ├── redis/                  # Redis client interface + go-redis implementation
│   └── resources/              # Kubernetes resource builders (STS, Dep, Svc, CM, PDB…)
└── .plan/                      # Architecture decisions and phase reports
```

---

## Version compatibility

| Operator version | Kubernetes | Redis |
|-----------------|------------|-------|
| 0.1.x (Phase 1) | 1.29 – 1.31 | 7.0 – 7.2 |

---