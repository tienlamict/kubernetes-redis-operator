# Kiến trúc hệ thống — Kubernetes Redis Operator

> Tài liệu này mô tả chi tiết kiến trúc của **Kubernetes Redis Operator** (module `github.com/example/redis-operator`, API group `redis.example.com/v1alpha1`) — một operator viết bằng Go/Kubebuilder v4 quản lý ba mô hình triển khai Redis trên Kubernetes: **Redis standalone**, **Redis Sentinel (HA)** và **Redis Cluster (sharded)**.

---

## 1. Tổng quan kiến trúc

### 1.1 Mục tiêu thiết kế

Operator cung cấp ba Custom Resource Definition (CRD) độc lập, mỗi CRD phản ánh một mô hình triển khai Redis khác nhau với vòng đời, topology và cơ chế phục hồi riêng:

| CRD | Mô hình | Điểm mạnh |
|-----|---------|-----------|
| `Redis` | Standalone (1 pod, có/không PVC) | Đơn giản, hot-reload config |
| `RedisSentinel` | Master + replicas + Sentinel quorum | Tự động failover qua Sentinel, role-based service routing |
| `RedisCluster` | Native Cluster (hash slot sharding) | Horizontal scaling, pre-triggered failover, slot rebalance tự động |

### 1.2 Sơ đồ các lớp thành phần

```
                ┌─────────────────────────────────────────┐
                │   User (kubectl apply <CR>.yaml)        │
                └────────────────────┬────────────────────┘
                                     │
                ┌────────────────────▼────────────────────┐
                │   Kubernetes API Server (etcd)          │
                │   - Redis / RedisSentinel / RedisCluster│
                │   - Webhooks (defaulting + validation)  │
                └────────────────────┬────────────────────┘
                                     │ watch
                ┌────────────────────▼────────────────────┐
                │   controller-runtime Manager (cmd/)     │
                │   ┌──────────────┬──────────────────┐   │
                │   │ Reconcilers  │  Metrics server  │   │
                │   │ (3 loops)    │  Health probes   │   │
                │   └──────┬───────┴──────────────────┘   │
                └──────────┼──────────────────────────────┘
                           │ build & apply
                ┌──────────▼──────────────┐   ┌──────────────────────┐
                │ internal/resources/     │   │ internal/redis/      │
                │  - ConfigMap / SVC      │   │  - RedisClient       │
                │  - StatefulSet / Deploy │   │  - ClusterManager    │
                │  - PDB / ServiceMonitor │   │  - SentinelManager   │
                └──────────┬──────────────┘   └──────────┬───────────┘
                           │ K8s resources             │ Redis protocol
                ┌──────────▼──────────────┐   ┌──────────▼───────────┐
                │  Kubernetes Workloads   │◄──┤  Redis pods          │
                │  (StS, SVC, CM, PDB…)   │   │  (master/replica/    │
                └─────────────────────────┘   │   sentinel nodes)    │
                                              └──────────────────────┘
```

Operator chạy bên trong cluster, quan sát các CR, build các resource mong muốn rồi đồng bộ với trạng thái thực tế (pattern "desired vs. observed"). Đối với các thao tác cần giao tiếp trực tiếp với Redis (CLUSTER MEET, SETSLOT, SENTINEL FAILOVER...) operator dùng `internal/redis/` như một thư viện wrapper.

---

## 2. Cấu trúc thư mục

```
.
├── api/v1alpha1/                 # CRD types + webhooks
│   ├── common_types.go           # StorageSpec, AuthSpec, TLSSpec, constants
│   ├── redis_types.go            # CRD Redis
│   ├── redissentinel_types.go    # CRD RedisSentinel
│   ├── rediscluster_types.go     # CRD RedisCluster
│   └── *_webhook.go              # Defaulting & validation webhooks
├── cmd/
│   └── main.go                   # Entry point, manager setup
├── config/                       # Kustomize: crd, rbac, samples, webhook
├── internal/
│   ├── controller/               # 3 reconcilers + envtest suite
│   ├── redis/                    # Redis client wrappers
│   │   ├── client.go             # RedisClient interface + go-redis impl
│   │   ├── cluster.go            # ClusterManager (bootstrap, migrate slots)
│   │   ├── sentinel.go           # SentinelManager
│   │   └── health.go             # Health helpers
│   ├── resources/                # Builders cho mọi Kubernetes resource
│   │   ├── configmap.go          # redis.conf, sentinel.conf, nodes.conf config
│   │   ├── statefulset.go        # StS builders (3 flavors)
│   │   ├── deployment.go         # Standalone + Sentinel Deployment
│   │   ├── service.go            # Headless + ClusterIP services
│   │   ├── pdb.go                # PodDisruptionBudget
│   │   ├── servicemonitor.go     # Prometheus ServiceMonitor
│   │   └── labels.go             # Label/selector conventions
│   └── metrics/                  # Prometheus metrics + recorder
├── scripts/
│   └── redis-shutdown.sh         # Pre-stop hook (SAVE + SENTINEL FAILOVER)
├── hack/                         # Boilerplate, codegen helpers
├── Dockerfile, Makefile, PROJECT
└── go.mod                        # Go 1.22, controller-runtime v0.19.3
```

---

## 3. API types (CRD)

Ba CRD nằm trong cùng API group `redis.example.com/v1alpha1`. Các kiểu dùng chung nằm ở [common_types.go](api/v1alpha1/common_types.go).

### 3.1 Kiểu chung ([common_types.go](api/v1alpha1/common_types.go))

- **`StorageSpec`**: `ClassName`, `Size`, `KeepAfterDeletion` — điều khiển PVC. Nếu `Storage == nil` (chỉ áp dụng cho `Redis` standalone) thì operator dùng `Deployment + emptyDir` thay vì `StatefulSet + PVC`.
- **`AuthSpec`**: tên Secret (phải chứa key `password`).
- **`TLSSpec`**: tên Secret chứa `tls.crt`, `tls.key`, `ca.crt`.
- **Hằng số quan trọng**:
  - `FinalizerName = "redis.example.com/finalizer"` — bảo đảm cleanup PVC / Sentinel RESET trước khi xoá CR.
  - Phase: `PhaseInitializing`, `PhaseReady`, `PhaseFailed`, `PhaseScaling`, `PhaseRecovering`.
  - Default images: `redis:7.2-alpine`, `oliver006/redis_exporter:latest`.
  - Labels chuẩn: `app.kubernetes.io/managed-by=redis-operator`, `app.kubernetes.io/part-of=redis`.

### 3.2 `Redis` ([redis_types.go](api/v1alpha1/redis_types.go))

| Field | Ý nghĩa |
|---|---|
| `Image` | Redis container image |
| `Resources` | CPU/mem requests/limits |
| `Storage` | Có PVC ⇒ StatefulSet; không có ⇒ Deployment + emptyDir |
| `RedisConfig` | Map override redis.conf (maxmemory, save,…) |
| `Auth`, `TLS` | Reference Secrets |
| `EnableExporter`, `ExporterImage` | Sidecar Prometheus exporter |
| `Affinity/Tolerations/NodeSelector` | Scheduling |

`RedisStatus` theo dõi `Phase`, `Conditions`, `RedisVersion` (detect runtime), `ReadyReplicas`.

### 3.3 `RedisSentinel` ([redissentinel_types.go](api/v1alpha1/redissentinel_types.go))

Spec gồm hai "bộ phận": Redis data plane và Sentinel control plane.

- `Replicas` ≥ 3 (mặc định 3): số Redis pod (1 master + N-1 replicas).
- `SentinelReplicas` ≥ 3 (mặc định 3): số Sentinel pod (lẻ để tạo quorum).
- Hai nhóm resource/image/config riêng: `Image/Resources/RedisConfig` vs `SentinelImage/SentinelResources/SentinelConfig`.
- Status phản ánh cả hai tầng: `MasterNode`, `ReadyReplicas`, `ReadySentinels`.

### 3.4 `RedisCluster` ([rediscluster_types.go](api/v1alpha1/rediscluster_types.go))

- `Masters` ≥ 3 (mặc định 3), `ReplicasPerMaster` ≥ 0 (mặc định 1).
- Cho phép override resource/scheduling **riêng cho master và replica**: `MasterResources/ReplicaResources`, `MasterAffinity/ReplicaAffinity`, v.v. — để tránh ràng buộc replica ngủ chung node với master.
- Status chi tiết mức topology:
  - Số `ReadyMasters`, `ReadyReplicas`.
  - `AssignedSlots` (healthy = 16384).
  - `ClusterState` = `"ok"` hoặc `"fail"` (từ `CLUSTER INFO`).
  - Mảng `Nodes []RedisNodeStatus`: mỗi phần tử có `NodeID` (40-char hex), `PodName`, `IP`, `Role`, `Slots`, `MasterRef`, `Health`.

### 3.5 Webhooks

Mỗi CRD có cặp `*_webhook.go` (defaulting + validation). Được register có điều kiện qua cờ `--enable-webhooks` (mặc định tắt để dev dễ hơn). Webhook đảm bảo:
- Điền giá trị mặc định (image, replicas, exporter on).
- Validate ràng buộc (tối thiểu replicas, quorum lẻ cho Sentinel, slot rule cho Cluster…).

---

## 4. `internal/redis/` — lớp truy cập Redis

Đây là lớp trừu tượng hoá mọi lệnh Redis operator cần. Tách thành interface giúp unit test dễ (xem [mock_redis_test.go](internal/controller/mock_redis_test.go)).

### 4.1 `RedisClient` ([client.go](internal/redis/client.go))

Interface bao trùm ba nhóm lệnh:

1. **Basic**: `Ping`, `Info`, `ConfigSet`, `ConfigGet`, `Save`.
2. **Cluster**: `ClusterInfo`, `ClusterNodes`, `ClusterMeet`, `ClusterAddSlots`, `ClusterDelSlots`, `ClusterReplicate`, `ClusterFailover`, `ClusterForget`, `ClusterReset`, `ClusterSetSlot` (với các subcommand `IMPORTING`/`MIGRATING`/`STABLE`/`NODE`), `ClusterGetKeysInSlot`, `ClusterCountKeysInSlot`, `Migrate`.
3. **Sentinel**: `SentinelMaster`, `SentinelGetMasterAddr`, `SentinelFailover`, `SentinelReset`.
4. **Replication**: `ReplicaOf`, `Role`.

`ClientOptions` chứa `Password`, `TLSConfig`, các timeout (Dial/Read/Write). Hàm `NewTLSConfig` tự load PEM và ép `MinVersion=TLS1.2`. Implementation thực tế dùng `github.com/redis/go-redis/v9`, client được tạo **per-call per-address** (pool nhỏ, phục vụ control plane chứ không phải data plane).

### 4.2 `ClusterManager` ([cluster.go](internal/redis/cluster.go))

Gói các thao tác mức cao trên RedisClient cho Cluster mode:

| Method | Vai trò |
|---|---|
| `FormCluster(masterAddrs, replicaAddrs, replicasPerMaster)` | Bootstrap cluster: MEET → wait known-nodes → chia đều 16384 slot → chờ `cluster_state:ok` → MEET replicas → round-robin `CLUSTER REPLICATE` |
| `AssignSlotsEvenly(masterAddrs)` | Chia 16384 slot đều cho N master |
| `MigrateSlot(src, dst, slot)` | Chuẩn quy trình 4 bước: `SETSLOT IMPORTING` trên dst → `SETSLOT MIGRATING` trên src → `GETKEYSINSLOT` + `MIGRATE` theo batch → `SETSLOT NODE` trên mọi node. Rollback về `STABLE` khi fail. |
| `MigrateSlotsRange`, `RebalanceSlots` | Cân bằng slot giữa donor/receiver khi scale |
| `AddMaster`, `RemoveMaster` | Thêm/bớt master, kết hợp rebalance + FORGET |
| `AddReplica`, `RemoveReplica` | Gắn/tách replica khỏi master |
| `GetClusterState`, `VerifySlotCoverage`, `WaitForClusterReady` | Observability/guard cho reconcile loop |

### 4.3 `SentinelManager` ([sentinel.go](internal/redis/sentinel.go))

Cung cấp `GetMaster`, `WaitForMaster`, `TriggerFailover`, `ResetMonitor`, `IsReplicaOfMaster`, `WaitForSentinelQuorum`. Operator dùng chúng để:
- Xác định master hiện tại để cập nhật label pod.
- Reset monitor sau khi delete CR.
- Poll quorum trong quá trình bootstrap.

### 4.4 `health.go`

Các helper wait/retry dùng chung (ví dụ polling `Ping`/`ClusterInfo` đến khi pass hoặc timeout).

---

## 5. `internal/resources/` — resource builders

Mỗi file xuất các hàm **thuần build** (không gọi API server) trả về object Kubernetes đã gắn sẵn labels, owner reference, annotation. Reconciler chịu trách nhiệm `CreateOrUpdate`.

### 5.1 Labels và selectors ([labels.go](internal/resources/labels.go))

Chuẩn hoá label sơ đồ `app.kubernetes.io/*` + `redis.example.com/instance`, `redis.example.com/component` (`redis`, `sentinel`, `master`, `replica`), `redis.example.com/role` (chỉ dùng cho Sentinel để phân biệt master/replica). Các helper `PodLabels`, `HeadlessServiceName`, `ClientServiceName` đảm bảo thống nhất trên toàn codebase.

### 5.2 ConfigMap ([configmap.go](internal/resources/configmap.go))

- `BuildRedisConfigMap` — redis.conf với default:
  ```
  bind 0.0.0.0
  protected-mode no
  dir /data
  appendonly yes
  save 3600 1 900 10 300 100
  port 6379          # hoặc tls-port 6379, tls-replication yes, tls-cluster yes
  ```
  Merge với `Spec.RedisConfig` override.
- `BuildSentinelRedisConfigMap` — tương tự nhưng cho Redis pods chạy dưới Sentinel.
- `BuildSentinelConfigMap` — `sentinel.conf`:
  ```
  sentinel monitor mymaster <master-ip> 6379 <quorum>
  sentinel down-after-milliseconds mymaster 5000
  sentinel failover-timeout mymaster 10000
  sentinel parallel-syncs mymaster 1
  ```
  Quorum = `SentinelReplicas/2 + 1`.
- `BuildClusterConfigMap` — bật cluster mode:
  ```
  cluster-enabled yes
  cluster-config-file nodes.conf
  cluster-node-timeout 15000
  cluster-announce-port 6379
  cluster-announce-bus-port 16379
  ```
  `cluster-announce-ip` inject ở runtime qua Downward API (env var `POD_IP`) để pod luôn announce đúng IP sau khi reschedule.

Tất cả builder dùng `renderConf` — sort keys, serialise `map[string]string` → `key value\n` để hash ổn định.

### 5.3 Deployment & StatefulSet

- **Standalone có storage**: `BuildRedisStatefulSet` — replicas=1, volumeClaimTemplate từ `StorageSpec`.
- **Standalone không storage**: `BuildRedisDeployment` — replicas=1, emptyDir `/data`.
- **Sentinel Redis pods**: `BuildSentinelRedisStatefulSet` — replicas=`Spec.Replicas`, inject env `SENTINEL_SVC` (FQDN của Sentinel service) và init container thiết lập replication.
- **Sentinel pods**: `BuildSentinelDeployment` — chạy `redis-server /etc/sentinel/sentinel.conf --sentinel`, port 26379, probe `redis-cli -p 26379 PING`.
- **Cluster master**: `BuildClusterMasterStatefulSet` — replicas=`Spec.Masters`, dùng `MasterResources/MasterAffinity`.
- **Cluster replica**: `BuildClusterReplicaStatefulSet` — replicas=`Spec.Masters * Spec.ReplicasPerMaster`.

Mọi pod template đều chèn **annotation `redis.example.com/config-hash`** (SHA-256 của ConfigMap data) — đây là cơ chế lõi cho **rolling restart do đổi config** (xem §7.1).

### 5.4 Service ([service.go](internal/resources/service.go))

- **Headless Service** (`ClusterIP: None`, `publishNotReadyAddresses: true`): cấp DNS ổn định cho StatefulSet pod, đồng thời cho phép cluster tự khám phá nhau ngay cả khi probe chưa ready (cần cho pha `CLUSTER MEET`).
- **ClusterIP Service** cho client access (port 6379).
- **Sentinel Master Service** — selector `role=master`; controller chịu trách nhiệm cập nhật nhãn.
- **Sentinel Replica Service** — selector `role=replica`.
- **Sentinel Service** — port 26379 cho client kết nối Sentinel.

### 5.5 PodDisruptionBudget ([pdb.go](internal/resources/pdb.go))

Mỗi nhóm pod (Sentinel Redis, Sentinel, Cluster master, Cluster replica) có PDB `maxUnavailable: 1` — đảm bảo drain/node upgrade không hạ đồng thời nhiều node cùng role.

### 5.6 ServiceMonitor ([servicemonitor.go](internal/resources/servicemonitor.go))

Khi `EnableExporter=true`, operator scrape exporter sidecar (port 9121) qua CRD `monitoring.coreos.com/v1`. Trường hợp không có Prometheus Operator, lỗi tạo ServiceMonitor được coi là non-fatal và chỉ log warning.

---

## 6. `internal/controller/` — các reconciler

Cả ba reconciler tuân pattern chung:

```
Reconcile → doReconcile →
  Load CR → handle deletion → ensure finalizer → init status
  → reconcile children (ConfigMap → Workload → Service → PDB → ServiceMonitor)
  → advanced logic (hot-reload, cluster lifecycle, role labels…)
  → update status
  → RequeueAfter 30s
```

Metrics được ghi thông qua wrapper `oprmetrics.RecordReconcile(kind, start, err)` để trích xuất duration và phân loại lỗi.

### 6.1 `RedisReconciler` ([redis_controller.go](internal/controller/redis_controller.go))

Dòng chảy trong `doReconcile`:
1. **Finalizer** — thêm `redis.example.com/finalizer`, requeue ngay.
2. **ConfigMap** — build, SHA-256 hash data → truyền xuống bước 3.
3. **Workload** — nếu có `Storage` ⇒ StatefulSet + Headless Service; nếu không ⇒ Deployment + emptyDir.
4. **Client Service** (ClusterIP) — điểm truy cập Redis.
5. **ServiceMonitor** — non-fatal.
6. **Hot-reload** — hàm `tryHotReload` kiểm tra tập thay đổi. Nếu mọi tham số đều hot-reloadable (vd: `maxmemory`, `maxmemory-policy`, `loglevel`) thì gọi `CONFIG SET` trực tiếp lên pod, tránh restart. Nếu có tham số cần reload cứng thì annotation hash sẽ đẩy StatefulSet rolling.
7. **Status** — ping pod, đọc `INFO server` lấy `redis_version`, cập nhật `Phase=Ready`.
8. **Deletion** — trước khi xoá finalizer: nếu `KeepAfterDeletion=false` thì xoá PVC `data-<name>-0`, phát event `Deleted`.

### 6.2 `RedisSentinelReconciler` ([redissentinel_controller.go](internal/controller/redissentinel_controller.go))

Thứ tự reconcile con:
1. ConfigMap cho Redis + ConfigMap cho Sentinel (quorum tính động).
2. StatefulSet Redis + Deployment Sentinel.
3. 4 services: Headless, SentinelMaster, SentinelReplica, Sentinel.
4. 2 PDB.
5. ServiceMonitor.
6. **`reconcileRoleLabels`** — bước đặc thù:
   - Gọi `SENTINEL GET-MASTER-ADDR-BY-NAME mymaster` trên Sentinel service.
   - Resolve IP → tìm pod Redis tương ứng.
   - Patch label `redis.example.com/role=master` lên pod đó, `role=replica` lên tất cả pod còn lại.
   - Nhờ đó, Service `SentinelMasterService` (selector `role=master`) và `SentinelReplicaService` (selector `role=replica`) luôn trỏ đúng.
7. Status: `MasterNode`, counts ready.

**Pre-stop hook** ([scripts/redis-shutdown.sh](scripts/redis-shutdown.sh)) được nhúng vào pod Redis:
1. `SAVE` snapshot.
2. Đọc `ROLE`.
3. Nếu là master & có env `SENTINEL_SVC`: gọi `SENTINEL FAILOVER mymaster`.
4. Sleep 5s để Sentinel hoàn tất bầu cử.
5. Cho phép `SIGTERM` tiến triển → graceful shutdown.

Khi xoá CR: trước khi xoá finalizer, operator gọi `SENTINEL RESET *` trên mọi Sentinel để chúng quên master cũ, rồi xoá PVC nếu cần.

### 6.3 `RedisClusterReconciler` ([rediscluster_controller.go](internal/controller/rediscluster_controller.go))

Đây là reconciler phức tạp nhất (~764 dòng). Sau khi reconcile các resource con (ConfigMap, 2 StatefulSet, 2 Headless + 1 Client service, 2 PDB, ServiceMonitor), controller gọi **`reconcileClusterLifecycle`** — máy trạng thái 5 nhánh:

| Điều kiện | Hành động |
|---|---|
| `Status.AssignedSlots == 0` (chưa init) | Chờ all pods ready → `ClusterManager.FormCluster` → phát event `ClusterFormed`, `Phase=Ready` |
| `ClusterState == "fail"` | `Phase=Recovering`, ghi metric `ClusterFailoverTotal`, requeue 15s, khi `state=ok` ⇒ quay về `Ready` |
| `Spec.Masters > currentMasters` | `reconcileScaleOut` |
| `Spec.Masters < currentMasters` | `reconcileScaleIn` |
| Bình thường | Update status + requeue 30s |

**`reconcileScaleOut`**:
1. `Phase=Scaling`, chờ master pods ready.
2. Với mỗi master mới: `ClusterManager.AddMaster(existingAddr, newAddr)` → `CLUSTER MEET`.
3. `RebalanceSlots` — di chuyển slot từ master cũ → master mới theo nguyên tắc cân bằng.
4. Thêm replica cho các master mới (`AddReplica`).
5. Ghi `ClusterScalingOperations{direction=out}` + duration.

**`reconcileScaleIn`**:
1. `Phase=Scaling`, chờ all pods ready.
2. Với mỗi master bị loại:
   - `RebalanceSlots` — đổ slot đi trước.
   - `RemoveMaster` — `CLUSTER RESET SOFT` + `CLUSTER FORGET` trên mọi node còn lại.
3. Tương tự cho replicas dư.

**`tryClusterRollingFailover`**:
- So sánh `config-hash` hiện tại vs hash trước đó.
- Nếu có tham số không hot-reloadable:
  - **Trước khi** StatefulSet rolling restart master pod, controller chủ động `CLUSTER FAILOVER` trên một replica của master đó → replica lên làm master → pod master cũ có thể bị restart an toàn.
  - Nhờ vậy downtime gần như bằng 0 trong chu kỳ rolling restart config.

**`updateClusterStatus`**:
- Probe từ pod master sẵn sàng.
- `GetClusterState` → parse `CLUSTER INFO` + `CLUSTER NODES`.
- Cập nhật `Phase`, `ReadyMasters`, `ReadyReplicas`, `AssignedSlots`, `ClusterState`, mảng `Nodes` (NodeID, IP, Role, Slots, MasterRef, Health).
- Ghi metric per-cluster (gauges + counters).

**Deletion**: xoá PVC các pod (nếu `KeepAfterDeletion=false`), gọi `oprmetrics.DeleteClusterMetrics(name, ns)` để dọn time-series tránh "ghost" metric tồn đọng, xoá finalizer.

### 6.4 `SetupWithManager`

Tất cả reconciler đều `Owns(StatefulSet/Deployment, Service, ConfigMap, PDB)` để tự requeue khi child object đổi. Cluster reconciler bổ sung watch các Pod để phát hiện pod restart ngay giữa quá trình scale.

---

## 7. Các cơ chế xuyên suốt

### 7.1 Config-hash driven rolling restart

Chuỗi sự kiện khi user đổi `spec.redisConfig`:

```
User edits CR → Reconcile →
  renderConf sorted → SHA-256 hash →
  ConfigMap updated (data) →
  PodTemplate.Annotations["redis.example.com/config-hash"] = <new hash> →
  StatefulSet controller phát hiện template thay đổi →
  rolling restart theo thứ tự pod-N, N-1, …, 0
```

Với Cluster, trước mỗi lần pod master bị restart, operator đã `CLUSTER FAILOVER` replica tương ứng → write path không đứt.

Với standalone, operator thử **hot-reload** (`CONFIG SET`) trước; chỉ rolling khi bắt buộc.

### 7.2 Finalizer & cleanup

`FinalizerName = "redis.example.com/finalizer"` gắn vào mọi CR. Khi CR bị xoá:
- **Redis**: xoá PVC `data-<name>-0` nếu không `KeepAfterDeletion`.
- **RedisSentinel**: `SENTINEL RESET *` → xoá PVCs.
- **RedisCluster**: xoá PVCs của tất cả StatefulSet, dọn metric per-cluster.

Tất cả đều best-effort (fail vẫn xoá finalizer) để tránh "stuck terminating".

### 7.3 Observability — metrics ([internal/metrics/metrics.go](internal/metrics/metrics.go))

| Metric | Dạng | Nhãn |
|---|---|---|
| `redis_operator_reconcile_total` | Counter | kind, result |
| `redis_operator_reconcile_duration_seconds` | Histogram | kind |
| `redis_operator_reconcile_errors_total` | Counter | kind, error_type |
| `redis_operator_managed_clusters` | Gauge | – |
| `redis_operator_managed_sentinels` | Gauge | – |
| `redis_operator_managed_standalone` | Gauge | – |
| `redis_operator_cluster_state` | Gauge | name, namespace |
| `redis_operator_cluster_assigned_slots` | Gauge | name, namespace |
| `redis_operator_cluster_known_nodes` | Gauge | name, namespace |
| `redis_operator_cluster_master_count` | Gauge | name, namespace |
| `redis_operator_cluster_replica_count` | Gauge | name, namespace |
| `redis_operator_cluster_scaling_operations_total` | Counter | name, namespace, direction |
| `redis_operator_cluster_scaling_duration_seconds` | Histogram | name, namespace, direction |
| `redis_operator_cluster_slot_migration_total` | Counter | name, namespace |
| `redis_operator_cluster_slot_migration_duration_seconds` | Histogram | name, namespace |
| `redis_operator_cluster_failover_total` | Counter | name, namespace |
| `redis_operator_cluster_failover_duration_seconds` | Histogram | name, namespace |

`DeleteClusterMetrics` được gọi khi xoá CR để tránh series rò rỉ.

### 7.4 Leader election & high-availability operator

`cmd/main.go` bật `--leader-elect` qua flag (mặc định `false`). Leader election ID: `redis-operator.example.com` — cho phép chạy nhiều replica operator, chỉ một pod là active reconciler.

### 7.5 Webhooks (điều kiện)

Cờ `--enable-webhooks` bật registration webhook (cần cert-manager để cấp serving cert). Webhook mutate (defaulting) + validate (min replicas, quorum lẻ, storage rule…) đảm bảo input không hợp lệ bị từ chối sớm trước khi đi vào reconcile loop.

---

## 8. `cmd/main.go` — điểm vào

1. **Scheme**: đăng ký core K8s + apps/v1 + policy/v1 + monitoring.coreos.com + redis.example.com/v1alpha1.
2. **Flags**: metrics/health/probe port, leader-elect, webhook port, enable-webhooks.
3. **Manager**: `ctrl.NewManager` với scheme, leader election, metrics server, health probes.
4. **Shared Redis infra**:
   ```go
   rc := redis.NewClient(redis.DefaultClientOptions())
   cm := redis.NewClusterManager(rc)
   ```
5. **Đăng ký 3 reconciler**:
   - `RedisReconciler` — không cần `RedisClient` (hot-reload dùng exec thông qua `kubectl`-free client nội bộ).
   - `RedisSentinelReconciler` — nhận client để gọi Sentinel.
   - `RedisClusterReconciler` — nhận cả `rc` và `cm`.
6. **Webhook** (có điều kiện).
7. **Health probes**: liveness & readiness `healthz.Ping`.
8. **Start**: `mgr.Start(ctrl.SetupSignalHandler())`.

---

## 9. `config/` — Kustomize

- `config/crd/bases/` — CRD manifests sinh từ `make manifests`.
- `config/rbac/role.yaml` — ClusterRole cho CR + child resources (StS, Deploy, SVC, CM, PVC, Events, PDB, ServiceMonitor).
- `config/manager/` — Deployment của operator.
- `config/samples/` — ba file mẫu: `redis_standalone.yaml`, `redis_sentinel.yaml`, `redis_cluster.yaml`.
- `config/webhook/` — cert-manager + webhook server config (dùng khi bật webhooks).

---

## 10. Testing

| File | Phạm vi |
|---|---|
| [suite_test.go](internal/controller/suite_test.go) | Bootstrap `envtest` (etcd + API server cục bộ) |
| [mock_redis_test.go](internal/controller/mock_redis_test.go) | Mock implementation của `RedisClient` cho unit test |
| [redis_controller_test.go](internal/controller/redis_controller_test.go) | Unit test RedisReconciler |
| [redis_integration_test.go](internal/controller/redis_integration_test.go) | Integration end-to-end dưới envtest |
| [redissentinel_controller_test.go](internal/controller/redissentinel_controller_test.go) | Sentinel reconcile + role label |
| [rediscluster_controller_test.go](internal/controller/rediscluster_controller_test.go) | Cluster lifecycle + scale |
| [cluster_test.go](internal/redis/cluster_test.go) | ClusterManager (mock RedisClient) |
| [api/v1alpha1/*_webhook_test.go](api/v1alpha1/) | Validation & defaulting |

Tiếp cận: envtest cho nhánh control-plane; mock `RedisClient` cho nhánh data-plane. Nhờ đó test không cần Redis thật nhưng vẫn phủ được logic phức tạp của slot migration / failover.

---

## 11. Các kịch bản phối hợp end-to-end

### 11.1 Tạo một `RedisCluster` mới

```
kubectl apply -f rediscluster.yaml
  ↓ API server persists CR
Reconciler.Reconcile
  ├─ thêm finalizer (requeue)
  ├─ status.Phase = Initializing
  ├─ build ConfigMap → apply  (nodes.conf + redis.conf, có cluster-enabled)
  ├─ build StatefulSet master (N replicas) + replica (N*R replicas)
  ├─ build Headless SVC + Client SVC + PDB + ServiceMonitor
  ├─ chờ mọi pod Ready
  ├─ ClusterManager.FormCluster:
  │    MEET all masters → wait known-nodes
  │    AssignSlotsEvenly 16384/N
  │    WaitForClusterReady (cluster_state=ok)
  │    MEET replicas → round-robin REPLICATE
  ├─ updateClusterStatus → AssignedSlots=16384, Phase=Ready
  └─ RequeueAfter 30s
```

Client dùng Client Service (ClusterIP) để kết nối bất kỳ node nào; node sẽ `MOVED`-redirect đến shard đúng như native Redis Cluster.

### 11.2 Failover tự động trong RedisSentinel

```
Master pod bị kill (node drain / OOM / …)
  ↓
Sentinel quorum phát hiện mất kết nối
  ↓ (down-after-milliseconds = 5s)
Sentinel bầu replica mới → promote → thông báo tất cả Sentinel
  ↓
Operator reconcile (trigger từ pod event / timer)
  ├─ reconcileRoleLabels:
  │    SENTINEL GET-MASTER-ADDR-BY-NAME → IP mới
  │    patch label role=master cho pod mới, role=replica cho pod còn lại
  └─ SentinelMasterService cập nhật endpoint (selector role=master)

Client mới sẽ được route tới master mới qua SentinelMasterService.
```

### 11.3 Scale-out `RedisCluster`

```
User chỉnh spec.masters: 3 → 5
  ↓
Reconciler phát hiện currentMasters < spec.masters → reconcileScaleOut
  ├─ StatefulSet master scale tới 5 replicas → pod 3, 4 khởi động
  ├─ chờ master pods ready
  ├─ AddMaster(existing, new) x 2 → CLUSTER MEET
  ├─ RebalanceSlots — move slot từ masters cũ sang mới
  │    (chu trình: IMPORTING/MIGRATING → GETKEYS → MIGRATE → SETSLOT NODE)
  ├─ AddReplica cho 2 master mới
  ├─ ghi metric scaling_duration, slot_migration_total
  └─ Phase=Ready
```

Trong suốt quá trình, cluster vẫn service được write (chỉ các slot đang migrate phải chịu redirect `ASK`).

### 11.4 Thay đổi redis.conf (không hot-reloadable) trên Cluster

```
User đổi spec.redisConfig.maxmemory-policy → cần restart
  ↓
Reconcile:
  ├─ ConfigMap data mới → hash SHA-256 đổi
  ├─ StatefulSet pod template annotation config-hash đổi
  ├─ tryClusterRollingFailover:
  │    cho mỗi master-i: chọn 1 replica của master-i → CLUSTER FAILOVER
  │    replica promote → master cũ giờ là replica
  ├─ StatefulSet rolling restart pod-N, N-1, … 0 (đã là replica → an toàn)
  └─ Sau restart, CLUSTER tự re-balance role qua gossip; operator có thể chủ động
     failover lại nếu cần khôi phục topology gốc
```

### 11.5 Xoá một `RedisCluster`

```
kubectl delete rediscluster demo
  ↓
DeletionTimestamp set
  ↓ Reconcile → handleDeletion:
  ├─ xoá PVCs của masters + replicas (nếu KeepAfterDeletion=false)
  ├─ oprmetrics.DeleteClusterMetrics(name, ns)
  ├─ RemoveFinalizer
  └─ API server xoá CR; owner references dọn StatefulSet/SVC/CM/PDB
```

---

## 12. Tổng kết các tính chất kiến trúc quan trọng

1. **Tách bạch trách nhiệm**: `api/` (shape), `resources/` (build), `redis/` (data-plane commands), `controller/` (orchestration), `metrics/` (observability), `cmd/` (wiring).
2. **Idempotent reconcile**: mọi bước đều là `CreateOrUpdate` + compare hash → an toàn khi bị retry.
3. **Desired-state driven bằng hash**: ConfigMap data → hash → annotation — không cần so sánh từng field để quyết định restart.
4. **Graceful failover**:
   - Sentinel mode: pre-stop hook + role-label routing.
   - Cluster mode: pre-triggered `CLUSTER FAILOVER` trước rolling update.
5. **Khả năng kiểm thử cao**: `RedisClient` và `ClusterManager` đều là interface ⇒ mock dễ, envtest chạy không cần Redis thật.
6. **Observability đầy đủ**: per-resource gauges + operation counters + scrape redis_exporter qua ServiceMonitor.
7. **Cleanup an toàn**: finalizer, tuỳ chọn giữ PVC, dọn series Prometheus khi delete.
8. **Mở rộng**: ba CRD độc lập, thêm CRD mới chỉ cần thêm một reconciler + builders tương ứng — không đụng lõi hiện có.

---
