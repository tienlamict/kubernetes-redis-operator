# Phân tích Architectural Mapping: Redis Lab ↔ Redis Operator

Tài liệu này đối chiếu kiến trúc giữa **Redis Lab** (interactive lab Kind + Bitnami Helm + Go/React, mô tả tại `D:\Project\Golang\redis-lab\redis-lab-prompt.md`) và **Redis Operator** hiện tại trong repo này. Mục tiêu duy nhất: bổ trợ design CRD / controller / event flow của Operator. Lab chỉ là observation tool — không so sánh "ai hơn ai".

## Section 0 — Codebase Inventory

**CRD types** (`api/v1alpha1/`):

- [redis_types.go:9](../api/v1alpha1/redis_types.go) — `RedisSpec`; [redis_types.go:61](../api/v1alpha1/redis_types.go) — `RedisStatus`
- [redissentinel_types.go:9](../api/v1alpha1/redissentinel_types.go) — `RedisSentinelSpec`; [redissentinel_types.go:81](../api/v1alpha1/redissentinel_types.go) — `RedisSentinelStatus`
- [rediscluster_types.go:9](../api/v1alpha1/rediscluster_types.go) — `RedisClusterSpec`; [rediscluster_types.go:89](../api/v1alpha1/rediscluster_types.go) — `RedisClusterStatus`; [rediscluster_types.go:126](../api/v1alpha1/rediscluster_types.go) — `RedisNodeStatus`

**Controllers** (`internal/controller/`):

- [redis_controller.go:53](../internal/controller/redis_controller.go) — `RedisReconciler`
- [redissentinel_controller.go:32](../internal/controller/redissentinel_controller.go) — `RedisSentinelReconciler`
- [rediscluster_controller.go:30](../internal/controller/rediscluster_controller.go) — `RedisClusterReconciler`

**Watch declarations** (cả 3 builder chỉ dùng `For(...).Owns(...)`, không có `Watches(...)` cho external resource):

- [redis_controller.go:427](../internal/controller/redis_controller.go) — For `Redis`; Owns StatefulSet, Deployment, Service, ConfigMap
- [redissentinel_controller.go:453](../internal/controller/redissentinel_controller.go) — For `RedisSentinel`; Owns STS, Deployment, Service, ConfigMap, PDB
- [rediscluster_controller.go:757](../internal/controller/rediscluster_controller.go) — For `RedisCluster`; Owns STS, Service, ConfigMap, PDB

**Redis client logic** (`internal/redis/`):

- [client.go:19](../internal/redis/client.go) — `RedisClient` interface (INFO/CONFIG/ROLE/CLUSTER */SENTINEL *)
- [sentinel.go:11](../internal/redis/sentinel.go) — `SentinelManager` (poll-based)
- [cluster.go](../internal/redis/cluster.go) — `ClusterManager` (form/scale/rebalance)

**MISSING**: Không có code subscribe pub/sub nào trong `internal/` (grep `Subscribe|PSUBSCRIBE` = 0 kết quả). Không có `Watches()` bổ sung cho Pod/Service bên ngoài owner refs. Không có helper `CLUSTER SHARDS` / `CLUSTER KEYSLOT`, không có MOVED-redirect logger.

---

## Section A — CRD Schema Mapping

### A.1 Lab topology → CRD field mapping

| Lab API field | Concept | Thuộc về | Operator location | Đánh giá |
|---|---|---|---|---|
| `master.pod` (standalone) | Identity master được elect | `.status` | — | 🤔 Redis CR là single-node nên không cần expose |
| `master.role`, `master.status` | Live role | `.status` | [redissentinel_controller.go:375](../internal/controller/redissentinel_controller.go) ghi `Status.MasterNode` | ✅ cho Sentinel; ❌ cho Cluster (chưa expose per-node live role) |
| `replicas[].replOffset` | Replication lag | `.status` | — | ❌ **Chưa có** trong cả 3 CRD |
| `sentinels[].monitoring`, `sentinels[].status` | Per-sentinel quorum state | `.status` | — | ❌ **Chưa có**. Chỉ có aggregate `ReadySentinels int32` ở [redissentinel_types.go:104](../api/v1alpha1/redissentinel_types.go) |
| `currentMaster` (Sentinel) | Master pod đang hoạt động | `.status` | [redissentinel_types.go:96](../api/v1alpha1/redissentinel_types.go) `MasterNode` | ✅ |
| `shards[].slotRanges` | Slot ownership per shard | `.status` | [rediscluster_types.go:141](../api/v1alpha1/rediscluster_types.go) `Slots string` (single range) | ⚠️ Có nhưng flattened — 1 string không biểu diễn được discontiguous ranges sau migration |
| `shards[].masterPod`, `shards[].replicas` | Shard composition | `.status` | [rediscluster_types.go:122](../api/v1alpha1/rediscluster_types.go) `Nodes []RedisNodeStatus` với `MasterRef` | ⚠️ Flat node list — caller phải tự group theo `MasterRef`. Chưa có `Shards []ShardStatus` aggregation |
| `redirects` (MOVED chain) | Client-side observability | `.status` | — | 🤔 **Không cần** — đây là client-debug data, không phải desired/observed state của cluster |

### A.2 Spec field analysis

Đối chiếu key trong Bitnami `values.yaml` với `.spec` Operator:

- `architecture: replication` → 🤔 implicit (kind CRD đóng vai discriminator).
- `auth.enabled` → covered: `Auth *AuthSpec` ([redis_types.go:31](../api/v1alpha1/redis_types.go)). Operator phong phú hơn (tham chiếu Secret).
- `sentinel.quorum`, `downAfterMilliseconds`, `failoverTimeout` → ❌ **Chưa promote thành typed field**. Chỉ có `SentinelConfig map[string]string` ([redissentinel_types.go:47](../api/v1alpha1/redissentinel_types.go)) catch theo dạng freeform. **RECOMMENDATION**: promote 3 key này thành typed fields có validation + default.
- `cluster.nodes`, `cluster.replicas` → Operator tách rõ thành `Masters` + `ReplicasPerMaster` ([rediscluster_types.go:13](../api/v1alpha1/rediscluster_types.go), [:18](../api/v1alpha1/rediscluster_types.go)). Cách đặt tên Operator rõ ràng hơn Bitnami's flat `nodes`.
- `image.tag` → `Image string` (image+tag gộp; ít granular hơn Bitnami).
- `resources` → ✅ Operator phong phú hơn (per-role overrides ở [rediscluster_types.go:31-35](../api/v1alpha1/rediscluster_types.go)).
- `replica.replicaCount` → covered bởi `Replicas` (Sentinel) / `ReplicasPerMaster` (Cluster).

### A.3 Hierarchical structure

Operator dùng **ba CRD song song**, lab dùng **ba namespace cùng chart Bitnami switch bằng values**. Trade-off:

- **Schema validation**: 3 CRDs thắng — type-specific OpenAPI validation, không có tổ hợp field vô lý (vd `SentinelReplicas` trên standalone).
- **RBAC granularity**: 3 CRDs thắng — scope verb riêng cho `redissentinels`.
- **Future extensibility**: ⚠️ 3 sibling CRD lặp `Image/Storage/Auth/TLS/EnableExporter/Affinity/...` 3 lần. **RECOMMENDATION**: extract một embedded `RedisPodTemplate` struct để cắt duplication mà không phải gộp CRD.

---

## Section B — Controller Decomposition

### B.1 Controller inventory

| Controller | File:line | Watch primary | Owns | Mode |
|---|---|---|---|---|
| `RedisReconciler` | [redis_controller.go:427](../internal/controller/redis_controller.go) | `Redis` | STS, Deploy, Svc, CM | standalone |
| `RedisSentinelReconciler` | [redissentinel_controller.go:453](../internal/controller/redissentinel_controller.go) | `RedisSentinel` | STS, Deploy, Svc, CM, PDB | sentinel |
| `RedisClusterReconciler` | [rediscluster_controller.go:757](../internal/controller/rediscluster_controller.go) | `RedisCluster` | STS, Svc, CM, PDB | cluster |

### B.2 Reconcile loop structure

Cả 3 đều theo cùng một pipeline phase implicit (không có abstraction `phase-step`): `Get → Deletion guard → Finalizer → init Phase → ConfigMap → Workloads → Services → [PDB] → [ServiceMonitor] → mode-specific → updateStatus → RequeueAfter 30s`. Decision point chính:

- Branching storage tại [redis_controller.go:113](../internal/controller/redis_controller.go) (STS vs Deploy).
- FSM cluster lifecycle tại [rediscluster_controller.go:305](../internal/controller/rediscluster_controller.go) (probe → form → fail → scale). Đây là chỗ duy nhất có Redis-state machine thực.

Cách tách per-mode của lab (`internal/redis/{standalone,sentinel,cluster}.go`) được Operator mirror ở **redis client layer**: có [internal/redis/sentinel.go](../internal/redis/sentinel.go) và [internal/redis/cluster.go](../internal/redis/cluster.go). Cùng pattern. Standalone không có file tương đương vì không có per-mode logic ngoài stock client.

### B.3 Refactor proposal

Khối `Owns(...)` lặp lại ở cả 3 controller; logic mode-specific thì không. Decomposition hiện tại **OK — ba controller tách rõ theo CRD kind**, và per-CRD owner khác nhau ít nhiều (cluster không có Deployment; standalone không có PDB). Duplication thực sự là common config typed trong spec — đã đề cập ở A.3, không phải vấn đề controller refactor.

---

## Section C — Resource Ownership & Event Flow

### C.1 Watched resources matrix

| Resource | Operator watch? | File:line | Lab quan sát? | Ghi chú |
|---|---|---|---|---|
| Pod | ❌ **Không `Watches/Owns`** | — | Có (qua informer) | Pods chỉ được tiếp cận qua `r.List()` trong `updateStatus` ([redis_controller.go:349](../internal/controller/redis_controller.go), [redissentinel_controller.go:380](../internal/controller/redissentinel_controller.go)) — không event-driven khi pod chết |
| StatefulSet | ✅ Owns | [:429](../internal/controller/redis_controller.go), [:455](../internal/controller/redissentinel_controller.go), [:759](../internal/controller/rediscluster_controller.go) | Không | |
| Deployment | ✅ Owns | [:430](../internal/controller/redis_controller.go), [:456](../internal/controller/redissentinel_controller.go) | Không | |
| Service | ✅ Owns | cả 3 | Không | |
| ConfigMap | ✅ Owns | cả 3 | Không | |
| Secret | ❌ Không watch | — | Không | `r.Get` on-demand tại [redis_controller.go:327](../internal/controller/redis_controller.go); đổi Secret không trigger reconcile |
| Sentinel pub/sub | ❌ **Không subscribe** | — | **Có (lab core feature)** | Gap quan trọng |
| Cluster bus / CLUSTER SHARDS | ❌ Chỉ polled trong `updateStatus` qua `GetClusterState` [rediscluster_controller.go:507](../internal/controller/rediscluster_controller.go) | — | Có (lab) | Chỉ biết sau mỗi 30s |

### C.2 Redis-level event handling

Operator biết failover **chỉ qua polling** — `SentinelGetMasterAddr` ở [redissentinel_controller.go:331](../internal/controller/redissentinel_controller.go) gọi mỗi reconcile (30s, RequeueAfter tại [:155](../internal/controller/redissentinel_controller.go)). Interface `SentinelManager` ([internal/redis/sentinel.go:11](../internal/redis/sentinel.go)) chỉ expose `GetMaster/WaitForMaster/TriggerFailover` — không có subscribe primitive.

**Race**: User kill master → Sentinel promote master mới trong vài giây → tối đa 30s `Status.MasterNode` và role label stale ([redissentinel_controller.go:321](../internal/controller/redissentinel_controller.go)). Tương tự cho Cluster: slot reassignment sau failover chỉ reflect trên `GetClusterState` lần sau, cũng 30s lag.

### C.3 Finalizer & ownership

- Finalizer name `redis.example.com/finalizer` tại [common_types.go:34](../api/v1alpha1/common_types.go); add ở mỗi reconciler.
- Owner refs set qua `ctrl.SetControllerReference(...)` trong mọi block `CreateOrUpdate`.
- Cleanup logic:
  - Standalone [redis_controller.go:151](../internal/controller/redis_controller.go) — delete single PVC `data-<name>-0` nếu `!KeepAfterDeletion`.
  - Sentinel [redissentinel_controller.go:160](../internal/controller/redissentinel_controller.go) — best-effort `SENTINEL RESET *` rồi PVC cleanup.
  - Cluster [rediscluster_controller.go:159](../internal/controller/rediscluster_controller.go) — PVC cleanup + Prometheus gauge cleanup. ❌ **Không có `SHUTDOWN SAVE`** trước khi delete pod; ❌ không migrate slot ra khỏi cluster trước khi teardown.

---

## P0 actions

1. **Add `Watches(&corev1.Pod{}, ...)` với label predicate** trong `RedisSentinelReconciler.SetupWithManager` ([redissentinel_controller.go:453](../internal/controller/redissentinel_controller.go)) và `RedisClusterReconciler` ([rediscluster_controller.go:757](../internal/controller/rediscluster_controller.go)) để pod deletion trigger reconcile ngay, thay vì poll 30s.
2. **Add Sentinel pub/sub subscriber** trong `internal/redis/sentinel.go` lắng `+switch-master`/`+sdown`/`+odown`, feed qua custom event source vào controller — thay polling-only `SentinelGetMasterAddr` tại [redissentinel_controller.go:331](../internal/controller/redissentinel_controller.go).
3. **Restructure `RedisClusterStatus.Nodes` thành `Shards []ShardStatus{Master, Replicas, SlotRanges}`** tại [rediscluster_types.go:89](../api/v1alpha1/rediscluster_types.go) — flat list hiện tại ở [:122](../api/v1alpha1/rediscluster_types.go) làm mất grouping theo shard mà lab expose tự nhiên.

**Câu hỏi mở lab chưa trả lời được**: graceful teardown ordering — Operator có nên drain slots (`CLUSTER FORGET` + migrate) trước khi delete cluster pods không, và sequence này tương tác thế nào với finalizer timeout khi PVC cần cleanup? Lab dùng `helm uninstall` (không migrate slot), nên không nói gì về shutdown sequencing đúng.
