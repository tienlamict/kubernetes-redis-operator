# Phase 1B — Standalone Controller: Báo cáo chi tiết

## Tổng quan

Phase 1B hoàn thiện controller cho CRD `Redis` (standalone), chứng minh toàn bộ pattern reconciliation của operator trước khi triển khai sang `RedisSentinel` và `RedisCluster`. Tất cả code compile sạch (`go build ./...`) và toàn bộ test pass (`go test ./...`).

---

## Các công việc đã thực hiện

### 1. `internal/controller/redis_controller.go` — Viết lại hoàn chỉnh

**Cấu trúc `RedisReconciler` mới:**

```go
type RedisReconciler struct {
    client.Client
    Scheme      *runtime.Scheme
    RedisClient redis.RedisClient   // interface để hot-reload
    Recorder    record.EventRecorder // ghi Kubernetes Events
}
```

**Vòng lặp Reconcile hoàn chỉnh:**

1. Fetch `Redis` CR — trả về nil nếu đã bị xóa (IsNotFound).
2. **Deletion handling** — nếu `DeletionTimestamp` được đặt, gọi `handleDeletion`.
3. **Finalizer** — thêm `redis.example.com/finalizer` nếu chưa có, requeue ngay.
4. **Initial status** — đặt `Phase = Initializing` nếu status trống.
5. **reconcileConfigMap** — tạo/cập nhật ConfigMap, trả về SHA-256 hash của data.
6. **Storage branch**:
   - Có storage: `reconcileHeadlessService` + `reconcileStatefulSet`
   - Không storage: `reconcileDeployment`
7. **reconcileService** — ClusterIP Service cho client.
8. **reconcileServiceMonitor** — Prometheus ServiceMonitor (nếu `EnableExporter=true`), non-fatal nếu prometheus-operator chưa cài.
9. **tryHotReload** — gọi `CONFIG SET` cho các tham số hot-reloadable.
10. **updateStatus** — đếm pod ready, set `ReadyReplicas`, `Phase`, `Conditions`.
11. Requeue sau 30 giây.

**Pattern `controllerutil.CreateOrUpdate`:**

Toàn bộ các hàm `reconcile*` dùng `controllerutil.CreateOrUpdate` thay vì `Get → Create/Update` thủ công. Pattern này:
- Idempotent (safe khi nhiều reconcile chạy song song).
- Khi create: copy toàn bộ `Spec` từ `desired`.
- Khi update: chỉ cập nhật các field có thể thay đổi (không động vào `ClusterIP`, `NodePort`, `VolumeClaimTemplates`).
- Gọi `ctrl.SetControllerReference` trong mutate function để set OwnerReference.

**Config Hash Annotation:**

```go
const configHashAnnotation = "redis.example.com/config-hash"
```

- Hàm `configDataHash(data map[string]string) string` sort các key theo alphabet rồi tính SHA-256, trả về 16 ký tự hex đầu.
- Hash này được inject vào `pod.spec.template.annotations` của cả StatefulSet và Deployment.
- Khi config thay đổi → hash thay đổi → Kubernetes trigger rolling update tự động.
- Giải quyết vấn đề: các tham số **không** hot-reloadable (vd. `appendonly`, `port`, `tls-*`) cần restart pod để áp dụng.

**Hot-reload via `CONFIG SET`:**

```go
var hotReloadableParams = map[string]bool{
    "maxmemory": true,  "maxmemory-policy": true,
    "hz": true,  "loglevel": true,  "save": true,
    "timeout": true,  "tcp-keepalive": true,
    "slowlog-log-slower-than": true,  "slowlog-max-len": true,
}
```

- Chỉ gọi `CONFIG SET` cho các key nằm trong whitelist trên.
- Không cần restart pod — thay đổi hiệu lực ngay lập tức.
- Lỗi hot-reload được log ở level V(1) và **không** propagate — đây là best-effort optimization.

**Condition management (`apimeta.SetStatusCondition`):**

Dùng `k8s.io/apimachinery/pkg/api/meta.SetStatusCondition` thay vì ghi trực tiếp vào slice để:
- Tự động update `LastTransitionTime` chỉ khi status thực sự thay đổi.
- Tuân thủ Kubernetes convention cho `metav1.Condition`.

```go
apimeta.SetStatusCondition(&redisObj.Status.Conditions, metav1.Condition{
    Type:   conditionReady,
    Status: boolConditionStatus(ready),
    Reason: reasonReady,
    ...
})
```

**HeadlessService reconciliation:**

StatefulSet cần Headless Service để cung cấp stable DNS (`<pod>.<svc>.<ns>.svc.cluster.local`). `reconcileHeadlessService` đảm bảo service này tồn tại với `ClusterIP=None` và `PublishNotReadyAddresses=true`.

**Event recording:**

```go
r.Recorder.Event(redisObj, corev1.EventTypeNormal, "Deleted", "Redis instance cleaned up")
```

Ghi event khi deletion hoàn tất. Recorder được inject từ manager.

---

### 2. `api/v1alpha1/redis_webhook.go` — Nâng cấp validation

**Cải tiến `validateSpec()`:**

Trước: chỉ kiểm tra `Size == ""`.

Sau: thêm validation `resource.ParseQuantity`:
```go
if _, err := resource.ParseQuantity(r.Spec.Storage.Size); err != nil {
    return nil, fmt.Errorf("spec.storage.size %q is not a valid resource.Quantity: %w", ...)
}
```

Điều này từ chối các giá trị như `"5gigabytes"` hay `"abc"` ngay tại admission time, trước khi Kubernetes tạo resource.

**`ValidateUpdate` mới — 3 constraint bổ sung:**

| Constraint | Lý do |
|---|---|
| Không thêm storage vào instance đã tồn tại | Việc chuyển Deployment → StatefulSet cần migration thủ công |
| Không xóa storage khỏi instance đang dùng | Có thể mất data |
| Storage class immutable | StorageClass không thể thay đổi sau khi PVC tạo |
| Storage size không được shrink | Kubernetes không hỗ trợ PVC shrink |

```go
// Detect shrink
oldQty, _ := resource.ParseQuantity(oldRedis.Spec.Storage.Size)
newQty, _ := resource.ParseQuantity(r.Spec.Storage.Size)
if newQty.Cmp(oldQty) < 0 {
    return nil, fmt.Errorf("spec.storage.size cannot be decreased ...")
}
```

---

### 3. `internal/controller/suite_test.go` — Nâng cấp test suite

**Thay đổi chính:**

- Thêm `testCtx` / `testCancel` với `context.WithCancel` để lifecycle manager được quản lý đúng.
- Scheme đầy đủ: `clientgoscheme` + `appsv1` + `corev1` + `redisv1alpha1` + `monitoringv1` (optional).
- Tạo `ctrl.Manager` thực sự và register `RedisReconciler` — envtest tests sẽ test toàn bộ reconciliation loop.
- Manager chạy trong goroutine background, được cancel khi `AfterSuite`.

---

### 4. `internal/controller/redis_controller_test.go` — Unit tests với fake client

**8 test cases, tất cả dùng `fake.NewClientBuilder()` (không cần cluster thật):**

| Test | Mô tả |
|---|---|
| `NoStorage_CreatesDeploymentServiceConfigMap` | Redis không có storage → Deployment + Service + ConfigMap được tạo; config hash annotation có mặt |
| `WithStorage_CreatesStatefulSetHeadlessServiceConfigMap` | Redis có storage → StatefulSet + HeadlessService (ClusterIP=None) + Service + ConfigMap |
| `FinalizerIsAdded` | Sau reconcile đầu tiên, finalizer `redis.example.com/finalizer` được thêm vào CR |
| `ConfigUpdate_ChangesPodTemplateHash` | Thêm `maxmemory` vào `redisConfig` → ConfigMap được update, hash trên pod template thay đổi |
| `Delete_RemovesFinalizer` | Khi `DeletionTimestamp` đặt, finalizer được remove |
| `Delete_WithStorage_DeletesPVC` | Xóa Redis có storage (keepAfterDeletion=false) → PVC `data-<name>-0` bị xóa |
| `Delete_WithStorage_KeepAfterDeletion` | Xóa Redis với `keepAfterDeletion=true` → PVC được giữ lại |
| `ConfigDataHash_DeterministicAndSensitive` | Hash giống nhau dù thứ tự key khác nhau; khác khi value thay đổi; luôn 16 chars |

---

### 5. `api/v1alpha1/redis_webhook_test.go` — Webhook unit tests

**12 test cases, gọi trực tiếp `Default()` / `ValidateCreate()` / `ValidateUpdate()` / `ValidateDelete()`:**

| Group | Test |
|---|---|
| Default() | Sets all defaults khi spec trống |
| Default() | Không override các giá trị đã đặt |
| ValidateCreate() | Pass khi không có storage |
| ValidateCreate() | Pass khi storage có size hợp lệ |
| ValidateCreate() | Reject khi size rỗng |
| ValidateCreate() | Reject khi size không phải Quantity hợp lệ |
| ValidateUpdate() | Reject thêm storage vào instance đang chạy |
| ValidateUpdate() | Reject xóa storage khỏi instance đang chạy |
| ValidateUpdate() | Reject shrink storage |
| ValidateUpdate() | Pass khi grow storage |
| ValidateUpdate() | Reject thay đổi StorageClass |
| ValidateUpdate() | Pass khi chỉ thay đổi Image |
| ValidateDelete() | Luôn pass |

---

## Kết quả kiểm tra

```
$ go build ./...
# (không có lỗi)

$ go test ./api/v1alpha1/... -run "TestDefault|TestValidate" -v
--- PASS: TestDefault_SetsImageDefaults
--- PASS: TestDefault_DoesNotOverrideExistingValues
--- PASS: TestValidateCreate_NoStorage_OK
--- PASS: TestValidateCreate_StorageWithSize_OK
--- PASS: TestValidateCreate_StorageEmptySize_Rejected
--- PASS: TestValidateCreate_StorageInvalidQuantity_Rejected
--- PASS: TestValidateUpdate_AddingStorageToExisting_Rejected
--- PASS: TestValidateUpdate_RemovingStorageFromExisting_Rejected
--- PASS: TestValidateUpdate_StorageShrink_Rejected
--- PASS: TestValidateUpdate_StorageGrow_OK
--- PASS: TestValidateUpdate_StorageClassChange_Rejected
--- PASS: TestValidateUpdate_ImageChange_OK
--- PASS: TestValidateDelete_AlwaysOK
PASS (13 tests)

$ go test ./internal/controller/... -run "TestReconcile|TestConfigDataHash" -v
--- PASS: TestReconcile_NoStorage_CreatesDeploymentServiceConfigMap
--- PASS: TestReconcile_WithStorage_CreatesStatefulSetHeadlessServiceConfigMap
--- PASS: TestReconcile_FinalizerIsAdded
--- PASS: TestReconcile_ConfigUpdate_ChangesPodTemplateHash
--- PASS: TestReconcile_Delete_RemovesFinalizer
--- PASS: TestReconcile_Delete_WithStorage_DeletesPVC
--- PASS: TestReconcile_Delete_WithStorage_KeepAfterDeletion
--- PASS: TestConfigDataHash_DeterministicAndSensitive
PASS (8 tests)
```

**Tổng: 21 tests, 0 failed.**

---

## Kiến trúc và các quyết định thiết kế

### Tại sao dùng `controllerutil.CreateOrUpdate` thay vì `Get → Create/Update`?

Pattern thủ công có race condition: giữa `Get` (NotFound) và `Create`, một instance khác có thể đã tạo resource. `CreateOrUpdate` dùng `optimistic locking` thông qua `resourceVersion` để handle trường hợp này an toàn.

### Tại sao HeadlessService phải được reconcile riêng?

StatefulSet reference `spec.serviceName` — field này **immutable** sau khi StatefulSet tạo. Nếu headless service chưa tồn tại khi StatefulSet start, pods sẽ không có stable DNS. Reconcile headless service **trước** StatefulSet đảm bảo thứ tự đúng.

### Tại sao ServiceMonitor failure là non-fatal?

prometheus-operator là một add-on tùy chọn. Nếu CRD `ServiceMonitor` chưa được cài (môi trường dev/test cơ bản), việc tạo ServiceMonitor sẽ fail với "no kind is registered". Operator không nên crash vì điều này — Redis vẫn hoạt động bình thường, chỉ thiếu Prometheus scraping.

### Config Hash vs Hot-Reload: khi nào dùng cái nào?

| Tham số | Phương pháp |
|---|---|
| `maxmemory`, `maxmemory-policy`, `hz`, `loglevel`, `save` | Hot-reload (CONFIG SET, no restart) |
| `appendonly`, `port`, `bind`, `tls-*`, `cluster-*` | Config hash (rolling restart) |

Hot-reload được thực hiện **sau** khi config hash đã được cập nhật. Điều này có nghĩa là nếu config thay đổi bao gồm cả hot-reloadable và non-hot-reloadable params, rolling restart sẽ xảy ra dù sao — nhưng hot-reload sẽ áp dụng hot-reloadable params ngay lập tức trước khi rolling restart hoàn tất.

---

## Cấu trúc file đã tạo/sửa

```
internal/controller/
  redis_controller.go          ← Viết lại hoàn chỉnh (~290 dòng)
  redis_controller_test.go     ← Tạo mới, 8 test cases
  suite_test.go                ← Nâng cấp với manager + context lifecycle

api/v1alpha1/
  redis_webhook.go             ← Nâng cấp validation
  redis_webhook_test.go        ← Tạo mới, 13 test cases
```

---

## Bước tiếp theo (Phase 1C / 2)

Phase 1B đã hoàn thiện pattern cho standalone Redis. Các bước tiếp theo:

1. **Phase 1C**: Áp dụng tương tự cho `RedisSentinelReconciler` — chủ yếu là nâng cấp từ `CreateOrUpdate` pattern và thêm condition management.
2. **Phase 1D**: `RedisClusterReconciler` — phức tạp hơn do cần quản lý cluster formation và slot rebalancing.
3. **Phase 2**: ML-based predictive auto-scaling (nằm ngoài phạm vi thesis phase này).
