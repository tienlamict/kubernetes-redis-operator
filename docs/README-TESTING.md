# Hướng dẫn Testing — Kubernetes Redis Operator

Tài liệu này mô tả tech stack test, danh mục test case hiện có, hướng dẫn chạy đầy đủ và đề xuất cải tiến cho repo `github.com/example/redis-operator`.

---

## 1. Tech stack & test framework

### 1.1 Tech stack chung

| Hạng mục | Phiên bản / Ghi chú |
|---|---|
| Ngôn ngữ | Go **1.22.0** ([go.mod](go.mod)) |
| Framework operator | [controller-runtime](https://pkg.go.dev/sigs.k8s.io/controller-runtime) **v0.19.3** |
| Scaffolding | Kubebuilder v4 ([PROJECT](PROJECT)) |
| Redis client | `github.com/redis/go-redis/v9` v9.7.0 |
| Metrics | `github.com/prometheus/client_golang` v1.20.5 |

### 1.2 Test framework

| Stack | Mục đích |
|---|---|
| **`go test`** (chuẩn) | Toàn bộ test entry point đều dùng `go test`. Các file webhook & unit dùng `t.Run` thuần. |
| **Ginkgo v2** (`github.com/onsi/ginkgo/v2` v2.22.0) | Cú pháp BDD `Describe/Context/It` cho integration tests trong `internal/controller/redis_integration_test.go`. |
| **Gomega** (`github.com/onsi/gomega` v1.36.0) | Matcher library. Unit test dùng pattern `g := NewWithT(t)`; integration test dùng `Expect`/`Eventually`. |
| **envtest** (`sigs.k8s.io/controller-runtime/pkg/envtest`) | Khởi động một control plane K8s thật (kube-apiserver + etcd nhị phân) cục bộ cho integration test. Phiên bản assets: **1.31.0** ([Makefile L4](Makefile)). |
| **client-go fake client** (`sigs.k8s.io/controller-runtime/pkg/client/fake`) | Test reconciler không cần envtest cho phần lớn unit test (nhanh hơn, không I/O). |
| **Test doubles tự xây** | `noopRedisClient`, `noopClusterManager`, `recordingRedisClient` (file `mock_redis_test.go`, `cluster_test.go`) — tránh phụ thuộc Redis thật. |

### 1.3 Script test có sẵn

Test entry point định nghĩa trong [Makefile](Makefile):

| Lệnh | Tác dụng |
|---|---|
| `make test` | (L43-45) Chạy `manifests`, `generate`, `fmt`, `vet` rồi `go test ./...` (loại trừ `./test/e2e`) với `-coverprofile cover.out`. Tự động cài `setup-envtest` qua target `envtest`. |
| `make test-e2e` | (L47-49) Chạy `go test ./test/e2e/ -v -ginkgo.v`. **Lưu ý: thư mục `test/e2e/` chưa tồn tại** — target này hiện không hoạt động (xem §5). |
| `make lint` | (L51-53) Chạy `golangci-lint`. |
| `make envtest` | (L128-131) Cài `setup-envtest` vào `./bin/`. |

Không có script CI (`.github/workflows/`), không có `.golangci.yml`, không có script test nào trong `scripts/` (chỉ có `redis-shutdown.sh` là pre-stop hook).

---

## 2. Danh mục test cases

Tổng cộng **10 file test**, **~91 test case**. Group theo file dưới đây.

### 2.1 Webhook tests (API package — pure unit, không cần envtest)

#### [api/v1alpha1/redis_webhook_test.go](api/v1alpha1/redis_webhook_test.go) — 13 cases

Defaulting và validation cho CRD `Redis` (standalone).

| # | Test | Mô tả |
|---|---|---|
| 1 | `TestDefault_SetsImageDefaults` | Set default image, exporter image, `enableExporter=true` |
| 2 | `TestDefault_DoesNotOverrideExistingValues` | Defaulting không ghi đè giá trị user đã set |
| 3 | `TestValidateCreate_NoStorage_OK` | Tạo Redis không có storage hợp lệ |
| 4 | `TestValidateCreate_StorageWithSize_OK` | Storage với size hợp lệ |
| 5 | `TestValidateCreate_StorageEmptySize_Rejected` | Reject khi storage size trống |
| 6 | `TestValidateCreate_StorageInvalidQuantity_Rejected` | Reject quantity không parse được |
| 7 | `TestValidateUpdate_AddingStorageToExisting_Rejected` | Không cho thêm storage sau khi tạo |
| 8 | `TestValidateUpdate_RemovingStorageFromExisting_Rejected` | Không cho gỡ storage |
| 9 | `TestValidateUpdate_StorageShrink_Rejected` | Không cho giảm size |
| 10 | `TestValidateUpdate_StorageGrow_OK` | Cho phép tăng size |
| 11 | `TestValidateUpdate_StorageClassChange_Rejected` | Không cho đổi StorageClass |
| 12 | `TestValidateUpdate_ImageChange_OK` | Cho phép đổi image |
| 13 | `TestValidateDelete_AlwaysOK` | Delete luôn hợp lệ |

#### [api/v1alpha1/rediscluster_webhook_test.go](api/v1alpha1/rediscluster_webhook_test.go) — 25 cases

Defaulting & validation cho CRD `RedisCluster`.

| # | Test | Mô tả |
|---|---|---|
| 1 | `TestClusterDefault_SetsAllDefaults` | Set defaults đầy đủ (image, exporter, masters=3, redis config) |
| 2 | `TestClusterDefault_DoesNotOverrideExistingValues` | Không ghi đè giá trị có sẵn |
| 3 | `TestClusterDefault_SetsAppendOnlyIfMissing` | Tự thêm `appendonly=yes` nếu thiếu |
| 4 | `TestClusterValidateCreate_ValidSpec_OK` | Spec đúng → OK |
| 5 | `TestClusterValidateCreate_MastersBelow3_Rejected` | Reject `masters < 3` |
| 6 | `TestClusterValidateCreate_NegativeReplicasPerMaster_Rejected` | Reject replicas âm |
| 7 | `TestClusterValidateCreate_TotalNodesExceedsLimit_Rejected` | Reject `total nodes > 100` |
| 8 | `TestClusterValidateCreate_EvenMasters_EmitsWarning` | Warning khi masters chẵn |
| 9 | `TestClusterValidateCreate_OddMasters_NoWarning` | Không warning khi masters lẻ |
| 10 | `TestClusterValidateCreate_StorageWithValidSize_OK` | Storage hợp lệ |
| 11 | `TestClusterValidateCreate_StorageEmptySize_Rejected` | Reject storage size trống |
| 12 | `TestClusterValidateCreate_StorageInvalidQuantity_Rejected` | Reject quantity sai |
| 13 | `TestClusterValidateUpdate_ValidUpdate_OK` | Update hợp lệ (đổi image) |
| 14 | `TestClusterValidateUpdate_MastersBelow3_Rejected` | Reject scale xuống `< 3` |
| 15 | `TestClusterValidateUpdate_ScaleDownByMoreThanOne_Rejected` | Reject scale-down nhiều hơn 1 master/lần |
| 16 | `TestClusterValidateUpdate_ScaleDownByOne_OK` | Cho phép scale-down 1 master |
| 17 | `TestClusterValidateUpdate_ScalingPhase_Rejected` | Reject update khi `Phase=Scaling` |
| 18 | `TestClusterValidateUpdate_AddStorageToExisting_Rejected` | Không cho thêm storage |
| 19 | `TestClusterValidateUpdate_RemoveStorageFromExisting_Rejected` | Không cho gỡ storage |
| 20 | `TestClusterValidateUpdate_StorageShrink_Rejected` | Không cho shrink |
| 21 | `TestClusterValidateUpdate_StorageGrow_OK` | Cho phép grow |
| 22 | `TestClusterValidateUpdate_StorageClassChange_Rejected` | Không cho đổi StorageClass |
| 23 | `TestClusterValidateDelete_ScalingPhase_Rejected` | Reject delete khi đang Scaling |
| 24 | `TestClusterValidateDelete_NormalPhase_OK` | Delete OK ở phase Ready |
| 25 | `TestClusterValidateDelete_EmptyPhase_OK` | Delete OK ở phase rỗng |

#### [api/v1alpha1/redissentinel_webhook_test.go](api/v1alpha1/redissentinel_webhook_test.go) — 19 cases

Defaulting & validation cho CRD `RedisSentinel`.

| # | Test | Mô tả |
|---|---|---|
| 1 | `TestSentinelDefault_SetsAllDefaults` | Defaults: image, exporter, replicas=3, sentinelReplicas=3 |
| 2 | `TestSentinelDefault_DoesNotOverrideExistingValues` | Không ghi đè |
| 3 | `TestSentinelValidateCreate_ValidSpec_OK` | Spec OK |
| 4 | `TestSentinelValidateCreate_ReplicasBelowThree_Rejected` | Reject `replicas < 3` |
| 5 | `TestSentinelValidateCreate_SentinelReplicasBelowThree_Rejected` | Reject `sentinelReplicas < 3` |
| 6 | `TestSentinelValidateCreate_EvenSentinelReplicas_Rejected` | Reject Sentinel chẵn (cần lẻ cho quorum) |
| 7 | `TestSentinelValidateCreate_ValidOddSentinel5_OK` | Sentinel = 5 OK |
| 8 | `TestSentinelValidateCreate_StorageWithValidSize_OK` | Storage hợp lệ |
| 9 | `TestSentinelValidateCreate_StorageEmptySize_Rejected` | Reject size trống |
| 10 | `TestSentinelValidateCreate_StorageInvalidQuantity_Rejected` | Reject quantity sai |
| 11 | `TestSentinelValidateUpdate_ValidUpdate_OK` | Update hợp lệ |
| 12 | `TestSentinelValidateUpdate_ScaleReplicasBelow3_Rejected` | Reject hạ replicas < 3 |
| 13 | `TestSentinelValidateUpdate_ScaleSentinelBelow3_Rejected` | Reject hạ sentinelReplicas < 3 |
| 14 | `TestSentinelValidateUpdate_AddStorageToExisting_Rejected` | Không cho thêm storage |
| 15 | `TestSentinelValidateUpdate_RemoveStorageFromExisting_Rejected` | Không cho gỡ storage |
| 16 | `TestSentinelValidateUpdate_StorageShrink_Rejected` | Không cho shrink |
| 17 | `TestSentinelValidateUpdate_StorageGrow_OK` | Cho phép grow |
| 18 | `TestSentinelValidateUpdate_StorageClassChange_Rejected` | Không cho đổi StorageClass |
| 19 | `TestSentinelValidateDelete_AlwaysOK` | Delete luôn OK |

### 2.2 Controller unit tests (fake client, không cần envtest)

#### [internal/controller/redis_controller_test.go](internal/controller/redis_controller_test.go) — 8 cases

| # | Test | Mô tả |
|---|---|---|
| 1 | `TestReconcile_NoStorage_CreatesDeploymentServiceConfigMap` | Không storage → tạo Deployment + Service + ConfigMap |
| 2 | `TestReconcile_WithStorage_CreatesStatefulSetHeadlessServiceConfigMap` | Có storage → StatefulSet + headless Service + ConfigMap |
| 3 | `TestReconcile_FinalizerIsAdded` | Finalizer được thêm trên reconcile đầu tiên |
| 4 | `TestReconcile_ConfigUpdate_ChangesPodTemplateHash` | Đổi `redisConfig` → annotation `config-hash` đổi → pod rollout |
| 5 | `TestReconcile_Delete_RemovesFinalizer` | Delete CR → finalizer được xoá |
| 6 | `TestReconcile_Delete_WithStorage_DeletesPVC` | PVC bị xoá khi `keepAfterDeletion=false` |
| 7 | `TestReconcile_Delete_WithStorage_KeepAfterDeletion` | PVC giữ lại khi `keepAfterDeletion=true` |
| 8 | `TestConfigDataHash_DeterministicAndSensitive` | Hash deterministic và đổi khi data đổi |

#### [internal/controller/rediscluster_controller_test.go](internal/controller/rediscluster_controller_test.go) — 11 cases

| # | Test | Mô tả |
|---|---|---|
| 1 | `TestClusterReconcile_AddsFinalizerOnFirstReconcile` | Thêm finalizer lần đầu |
| 2 | `TestClusterReconcile_CreatesAllCoreResources` | Tạo ConfigMap + master STS + replica STS + 2 headless SVC + client SVC + 2 PDB |
| 3 | `TestClusterReconcile_ConfigHashInjectedInMasterStatefulSet` | Annotation hash có mặt trên master STS |
| 4 | `TestClusterReconcile_ConfigUpdateChangesHash` | Đổi config → hash đổi |
| 5 | `TestClusterReconcile_Delete_RemovesFinalizer` | Delete → finalizer được xoá |
| 6 | `TestClusterReconcile_Delete_WithStorage_DeletesPVCs` | PVCs (master + replica) bị xoá |
| 7 | `TestClusterReconcile_Delete_KeepAfterDeletion_PreservesPVCs` | PVC giữ lại khi flag bật |
| 8 | `TestClusterReconcile_NilClusterManager_SkipsLifecycle` | Khi `ClusterManager == nil`: vẫn tạo resource, nhưng không đẩy `Phase=Ready` |
| 9 | `TestClusterRollingFailover_RecordsLastConfigHash` | Annotation `last-config-hash` được lưu để so sánh lần sau |
| 10 | `TestClusterRollingFailover_TriggersFailoverOnNonHotReloadableChange` | Đổi config không hot-reloadable → operator gọi `CLUSTER FAILOVER` trước restart |
| 11 | `TestClusterReconcile_ReplicaStatefulSet_OnlyWhenReplicasPerMasterGtZero` | Replica STS chỉ tạo khi `replicasPerMaster > 0` |

#### [internal/controller/redissentinel_controller_test.go](internal/controller/redissentinel_controller_test.go) — 10 cases

| # | Test | Mô tả |
|---|---|---|
| 1 | `TestSentinelReconcile_AddsFinalizerOnFirstReconcile` | Thêm finalizer |
| 2 | `TestSentinelReconcile_CreatesAllCoreResources` | Tạo Redis CM + Sentinel CM + Redis STS + Sentinel Deployment + 4 Service |
| 3 | `TestSentinelReconcile_ConfigHashInjectedInStatefulSet` | Hash annotation present |
| 4 | `TestSentinelReconcile_ConfigUpdateChangesHash` | Đổi config → hash đổi |
| 5 | `TestSentinelReconcile_Delete_RemovesFinalizer` | Delete → finalizer xoá |
| 6 | `TestSentinelReconcile_MasterServiceSelector_UsesRoleLabel` | Master Service selector chứa `role=master` |
| 7 | `TestSentinelReconcile_ReplicaServiceSelector_UsesRoleLabel` | Replica Service selector chứa `role=replica` |
| 8 | `TestSentinelReconcile_RoleLabels_TracksActualMaster` | Pod label theo dõi đúng IP master từ Sentinel |
| 9 | `TestSentinelReconcile_Delete_CallsSentinelReset` | Cleanup gọi `SENTINEL RESET *` |
| 10 | `TestSentinelReconcile_RoleLabels_FallbackToPodZero` | Khi `RedisClient == nil`, fallback gán pod-0 làm master |

#### [internal/controller/mock_redis_test.go](internal/controller/mock_redis_test.go) — Test doubles, không có test case

Implement `noopRedisClient` và `noopClusterManager` phục vụ các file unit test ở trên. Theo dõi số lần gọi `SentinelReset`, `ClusterFailover`.

### 2.3 Integration tests (envtest)

#### [internal/controller/suite_test.go](internal/controller/suite_test.go) — Setup, không có test case

- `TestControllers(t)` là entry duy nhất (`RegisterFailHandler` + `RunSpecs`).
- `BeforeSuite`: dựng `envtest.Environment`, cài CRD từ `config/crd/bases`, build scheme, đăng ký 3 reconciler, start Manager.
- `AfterSuite`: cancel context, stop envtest (xử lý lỗi signal trên Windows).
- Resolve `KUBEBUILDER_ASSETS` qua biến môi trường hoặc `setup-envtest use 1.31.0`.

#### [internal/controller/redis_integration_test.go](internal/controller/redis_integration_test.go) — 5 `It` blocks

Ginkgo suite `Describe("Redis controller (integration)", Ordered, ...)` chia 5 nhóm namespace tách biệt. Eventually timeout = 20s, interval = 250ms.

| # | Context | It | Mô tả |
|---|---|---|---|
| 1 | standalone Redis without storage | `creates Deployment, Service and ConfigMap` | Verify 3 resource và annotation `config-hash` |
| 2 | standalone Redis without storage | `rolls the Deployment when redisConfig changes` | Đổi config → hash đổi → Deployment roll |
| 3 | standalone Redis with storage | `creates StatefulSet and headless Service` | Verify STS + headless SVC |
| 4 | finalizer lifecycle | `adds finalizer on first reconcile` | Finalizer xuất hiện sau reconcile đầu |
| 5 | RedisSentinel resource creation | `creates Redis StatefulSet, Sentinel Deployment and all Services` | Verify đầy đủ resource Sentinel |
| 6 | RedisCluster resource creation | `creates master StatefulSet, client Service, and PodDisruptionBudgets` | Verify resource Cluster |

> Ghi chú: integration test **không** start Redis thật — chỉ kiểm tra controller-runtime tạo đúng resource trên control plane. Logic giao tiếp với Redis (CLUSTER MEET, slot migration…) được kiểm tra qua unit test với mock.

### 2.4 Redis client unit tests

#### [internal/redis/cluster_test.go](internal/redis/cluster_test.go) — 2 cases

| # | Test | Mô tả |
|---|---|---|
| 1 | `TestMigrateSlot_HappyPath` | Migration thành công: chỉ phát `IMPORTING` → `MIGRATING` → `NODE` (không có `STABLE`) |
| 2 | `TestMigrateSlot_SetsSlotStableOnKeyMigrationFailure` | Khi `MIGRATE` lỗi: gọi `CLUSTER SETSLOT STABLE` trên cả source và destination để rollback |

### 2.5 Tổng hợp

| Loại | File | Số case |
|---|---|---|
| Webhook unit | 3 | 57 |
| Controller unit (fake client) | 3 | 29 |
| Integration (envtest, Ginkgo) | 1 | 5 |
| Redis client unit | 1 | 2 |
| **Tổng** | **8** | **93** |

(2 file còn lại là `suite_test.go` setup và `mock_redis_test.go` test doubles.)

---

## 3. Hướng dẫn chạy test

### 3.1 Yêu cầu môi trường

| Yêu cầu | Phiên bản | Cài đặt |
|---|---|---|
| Go | ≥ 1.22 | <https://go.dev/dl/> |
| make | bất kỳ | đi kèm Linux/macOS, Windows dùng `choco install make` hoặc Git-Bash |
| Docker (chỉ cho `docker-build`) | optional | – |

`make envtest` sẽ tự động `go install sigs.k8s.io/controller-runtime/tools/setup-envtest` vào `./bin/` và download Kubernetes 1.31.0 binaries (kube-apiserver, etcd, kubectl) lần đầu chạy.

### 3.2 Chạy toàn bộ test (đề xuất)

```bash
make test
```

Chuỗi target sẽ chạy lần lượt: `manifests` → `generate` → `fmt` → `vet` → `envtest` (cài assets nếu chưa có) → `go test ./... -coverprofile cover.out` (loại trừ `./test/e2e`).

Sau khi hoàn tất, file `cover.out` được tạo ở thư mục gốc.

### 3.3 Chạy theo phạm vi nhỏ hơn

```bash
# Chỉ webhook tests (nhanh, không cần envtest)
go test ./api/v1alpha1/... -v

# Chỉ controller unit tests dùng fake client
go test ./internal/controller/ -run 'TestReconcile|TestCluster|TestSentinel' -v

# Chỉ Redis client cluster_test
go test ./internal/redis/ -v

# Chỉ integration tests (Ginkgo, cần envtest assets)
KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.31.0 -p path)" \
  go test ./internal/controller/ -run TestControllers -v
```

### 3.4 Chạy một test case cụ thể

```bash
# Chạy đúng 1 test Go (webhook / controller unit)
go test ./api/v1alpha1/ -run TestValidateUpdate_StorageGrow_OK -v

# Chạy 1 spec Ginkgo cụ thể qua focus
KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.31.0 -p path)" \
  go test ./internal/controller/ -run TestControllers -v \
  -ginkgo.focus="rolls the Deployment when redisConfig changes"
```

### 3.5 Cài envtest assets thủ công

Lần đầu (hoặc khi đổi version):

```bash
# Cài setup-envtest
make envtest

# Download assets K8s 1.31.0 và in path
./bin/setup-envtest use 1.31.0 -p path

# Export thủ công nếu chạy `go test` ngoài make
export KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.31.0 -p path)"
```

Trên Windows PowerShell:

```powershell
$env:KUBEBUILDER_ASSETS = (& .\bin\setup-envtest.exe use 1.31.0 -p path)
go test .\internal\controller\... -v
```

### 3.6 Chạy với coverage

```bash
# Sinh cover.out và xem tóm tắt
make test
go tool cover -func=cover.out

# Xem báo cáo HTML
go tool cover -html=cover.out -o cover.html
```

### 3.7 Chạy với race detector

```bash
KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.31.0 -p path)" \
  go test -race ./...
```

(Hiện Makefile **chưa** có target `-race`; xem §5 đề xuất bổ sung `make test-race`.)

### 3.8 Verbose, đếm lần chạy, timeout

```bash
# In log mỗi test
go test ./... -v

# Vô hiệu hoá test cache, chạy lại sạch
go test -count=1 ./...

# Timeout cho integration test (mặc định Go là 10 phút)
go test ./internal/controller/ -run TestControllers -timeout 3m -v
```

### 3.9 Lint

```bash
make lint        # chạy golangci-lint
make lint-fix    # tự sửa
```

> Hiện chưa có file `.golangci.yml`; golangci-lint sẽ dùng default. Xem §5 đề xuất.

### 3.10 E2E (chưa khả dụng)

Target `make test-e2e` trỏ đến thư mục `./test/e2e/` — thư mục này **chưa tồn tại** trong repo. Hiện không chạy được. Xem đề xuất cải tiến §5.4.

### 3.11 Troubleshooting

| Triệu chứng | Nguyên nhân | Khắc phục |
|---|---|---|
| `unable to find kubebuilder assets` | Không export `KUBEBUILDER_ASSETS` | Chạy `make envtest`, hoặc export thủ công như §3.5 |
| `address already in use` khi chạy lại | Tiến trình kube-apiserver/etcd cũ còn sống | `pkill -f kube-apiserver`, `pkill -f etcd` (Linux/macOS); Task Manager (Windows) |
| Test treo trên Windows | envtest không gửi được signal SIGINT đúng cách | Đã có handle trong `AfterSuite`; nếu vẫn treo → tăng timeout `-timeout 5m` |
| `controller-gen: command not found` | Chưa cài | `make manifests` sẽ tự `go install` |
| `Eventually timed out after 20s` | Reconcile chậm khi máy yếu | Tăng `Eventually(..., 60*time.Second, ...)` hoặc set env `INTEGRATION_TIMEOUT=60s` (cần thêm code, xem §5) |

---

## 4. Quy ước viết test mới

Để giữ test base nhất quán:

1. **Webhook** → đặt cạnh type ở `api/v1alpha1/<resource>_webhook_test.go`, dùng `t.Run` + `NewWithT(t)`.
2. **Controller logic không cần API server** → fake client + `t.Run` ở `internal/controller/<resource>_controller_test.go`.
3. **Hành vi end-to-end cần API server** → Ginkgo `It` ở `internal/controller/redis_integration_test.go`. Mỗi nhóm dùng namespace riêng `integ-<feature>` để tránh va chạm giữa các spec `Ordered`.
4. **Redis-protocol logic** → unit test ở `internal/redis/*_test.go` với test doubles cục bộ. Không gọi go-redis thật.
5. Mọi test phải chạy không cần Internet (sau khi `setup-envtest` cache assets).
6. Tên test mô tả hành vi, không mô tả chi tiết: `TestReconcile_<Condition>_<ExpectedOutcome>` cho `t.Run`, `It("creates ...")` cho Ginkgo.

---

## 5. Đề xuất cải tiến

Phần này liệt kê các thiếu sót quan sát được và đề xuất ưu tiên.

### 5.1 Thêm `make` target hỗ trợ workflow phổ biến

Đề xuất bổ sung vào Makefile:

```make
.PHONY: test-race
test-race: manifests generate fmt vet envtest ## Run tests with race detector.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-path $(LOCALBIN)/k8s -p path)" \
		go test -race $$(go list ./... | grep -v /e2e) -coverprofile cover.out

.PHONY: test-unit
test-unit: ## Run only fast unit tests (no envtest).
	go test ./api/... ./internal/redis/... -count=1 -v

.PHONY: test-integration
test-integration: manifests generate envtest ## Run only Ginkgo integration tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-path $(LOCALBIN)/k8s -p path)" \
		go test ./internal/controller/ -run TestControllers -v -timeout 5m

.PHONY: cover-html
cover-html: ## Open coverage report in browser.
	go tool cover -html=cover.out

.PHONY: cover-func
cover-func: ## Print per-function coverage to stdout.
	go tool cover -func=cover.out
```

**Ưu tiên:** Cao — chi phí thấp, lợi ích lớn cho dev experience.

### 5.2 Bật race detector & coverage gate trong CI

`make test` hiện không bật `-race` và không có ngưỡng coverage. Đề xuất:

- Thêm `-race` vào target `test` mặc định (hoặc tạo `test-race` riêng và chạy ở CI).
- Sau `go test ... -coverprofile cover.out`, parse `go tool cover -func=cover.out` và fail CI nếu **total < 70%**. Có thể dùng [`go-cover-treemap`](https://github.com/nikolaydubina/go-cover-treemap) cho visualisation.

**Ưu tiên:** Cao.

### 5.3 Thêm GitHub Actions workflow

Hiện không có `.github/workflows/`. Đề xuất tạo `.github/workflows/ci.yaml`:

```yaml
name: CI
on:
  pull_request:
  push:
    branches: [main, dev]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.25.10' }
      - run: make lint
      - run: make test
      - uses: actions/upload-artifact@v4
        with: { name: coverage, path: cover.out }
```

**Ưu tiên:** Cao.

### 5.4 Khôi phục target `make test-e2e`

Target hiện trỏ vào `./test/e2e/` không tồn tại. Hai lựa chọn:

- **Option A (đơn giản):** Xoá target khỏi Makefile cho đến khi có e2e thật.
- **Option B (đề xuất):** Tạo skeleton `test/e2e/` dùng [Kuttl](https://kuttl.dev/) hoặc Ginkgo + KinD. Test typical flow: `kubectl apply -f sample` → wait `Phase=Ready` → port-forward → `redis-cli SET/GET` → delete CR.

**Ưu tiên:** Trung bình — phù hợp cho Phase 1B/1C.

### 5.5 Test parallel để giảm wall-time

Không có `t.Parallel()` trong codebase. Các webhook test và unit test với fake client an toàn cho song song. Đề xuất thêm `t.Parallel()` ở:

- Tất cả `t.Run` trong `api/v1alpha1/*_webhook_test.go` (đều chỉ thao tác struct trong RAM).
- Các test reconciler unit nếu mỗi test tự build `fakeClient` riêng (đã đúng thiết kế).

Lưu ý: integration test Ginkgo `Ordered` không nên parallel.

**Ưu tiên:** Thấp — chỉ giúp tăng tốc khi suite lớn dần.

### 5.6 Cấu hình `golangci-lint`

Repo không có `.golangci.yml`. Đề xuất tạo file ở root:

```yaml
run:
  timeout: 5m
  go: '1.22'
linters:
  enable:
    - gofmt
    - goimports
    - govet
    - staticcheck
    - errcheck
    - ineffassign
    - unused
    - misspell
    - gocyclo
    - revive
issues:
  exclude-rules:
    - path: _test\.go
      linters: [gocyclo, errcheck]
```

**Ưu tiên:** Trung bình.

### 5.7 Tăng độ phủ test ở các vùng yếu

Quan sát từ inventory §2, các vùng dưới đây hiện chưa có test (hoặc chỉ có 1-2 case):

| Module | Tình trạng | Đề xuất |
|---|---|---|
| `internal/redis/client.go` | Chỉ có `cluster_test.go` test 2 case | Test wrapper `Ping`, `Info`, `ConfigSet`, `ClusterInfo` parse; có thể dùng `miniredis` cho test in-process. |
| `internal/redis/sentinel.go` | Không có test | Test `WaitForMaster`, `IsReplicaOfMaster`, `WaitForSentinelQuorum` qua mock. |
| `internal/redis/cluster.go` (`FormCluster`, `RebalanceSlots`) | Chỉ có `MigrateSlot` | Bổ sung test cho `FormCluster` happy path & lỗi `WaitForKnownNodes`, `RebalanceSlots` donor/receiver. |
| `internal/resources/*` | Không có test trực tiếp | Test thuần build object: hash ổn định, label đúng, owner reference set, TLS rule render đúng. |
| `internal/metrics/recorder.go` | Không có test | Test `RecordReconcile`, `DeleteClusterMetrics` không panic, label cardinality đúng. |
| `internal/controller/redis_integration_test.go` | 5 cases | Bổ sung scenario: rolling restart trên Cluster trigger `CLUSTER FAILOVER`, scale-out cluster qua envtest (mock RedisClient). |

**Ưu tiên:** Cao cho `resources/`, `redis/sentinel.go`; trung bình cho phần còn lại.

### 5.8 Stress / flake guard

Một số integration test phụ thuộc `Eventually(..., 20*time.Second)`. Trên CI tải nặng có thể flake. Đề xuất:

- Đưa timeout/interval thành biến cấu hình theo env: `INTEGRATION_TIMEOUT`, `INTEGRATION_INTERVAL`.
- Thêm `-ginkgo.flake-attempts=2` vào CI để giảm noise tạm thời, đồng thời track flake qua artefact.

**Ưu tiên:** Thấp.

### 5.9 Tài liệu test

- Liên kết file này (`README-TESTING.md`) từ `README.md` chính trong section "Development".
- Thêm badge coverage (Codecov hoặc Coveralls) sau khi có CI.

**Ưu tiên:** Thấp — cosmetic.

---

## 6. Tóm tắt nhanh

```bash
# 1. Cài đặt lần đầu
make envtest                                     # download K8s assets

# 2. Chạy đầy đủ
make test                                        # unit + integration + coverage

# 3. Chạy nhanh phần unit
go test ./api/... ./internal/redis/... -count=1 -v

# 4. Chạy integration
KUBEBUILDER_ASSETS="$(./bin/setup-envtest use 1.31.0 -p path)" \
  go test ./internal/controller/ -run TestControllers -v

# 5. Coverage HTML
go tool cover -html=cover.out -o cover.html
```

---