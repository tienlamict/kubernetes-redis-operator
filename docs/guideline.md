# Hướng dẫn chạy Kubernetes Redis Operator (PowerShell)

Tài liệu này hướng dẫn **từng bước** cách chạy operator từ source trên **Windows + PowerShell**, **không dùng `make`**, gọi thẳng `go` và các tool đi kèm. Mỗi câu lệnh đều có giải thích để hiểu rõ tác dụng.

> **Vì sao không dùng `make`?** [Makefile](Makefile) khai báo `SHELL = /usr/bin/env bash` và dùng `$(shell pwd)` — chỉ chạy được trong Git Bash/WSL. Trên CMD/PowerShell sẽ lỗi `CreateProcess(NULL, pwd, ...) failed`. Tài liệu này thay thế từng target `make` bằng lệnh tương đương.

Có 3 kịch bản chạy chính:

| Kịch bản | Khi nào dùng |
|---|---|
| **A. Chạy local (out-of-cluster)** | Phát triển hằng ngày, debug nhanh — operator chạy trên máy host, kết nối cluster qua `~/.kube/config`. |
| **B. Chạy trên Kind/Minikube (in-cluster)** | Test full flow — build image, load vào cluster local, deploy như Deployment thật. |
| **C. Deploy lên cluster thật** | Production/staging — push image lên registry. |

---

## 0. Yêu cầu môi trường

| Tool | Version tối thiểu | Mục đích |
|---|---|---|
| **PowerShell** | 5.1 hoặc 7+ | Shell chính |
| **Go** | 1.22 | Build/run operator binary |
| **Docker Desktop** | 20.10+ | Build image (kịch bản B, C) |
| **kubectl** | matching cluster | Apply CRD, kiểm tra resource |
| **Kind** *(option)* | 0.23+ | Tạo cluster K8s local cho kịch bản B |
| **Minikube** *(option)* | 1.33+ | Thay thế Kind |

Kiểm tra nhanh trong PowerShell:

```powershell
go version          # → go version go1.22.x windows/amd64
docker version
kubectl version --client
$PSVersionTable.PSVersion
```

> Mọi lệnh trong tài liệu giả định bạn đang ở **thư mục root của repo**:
> ```powershell
> cd D:\Project\Golang\kubernetes-redis-operator
> ```

---

## 1. Clone & tải dependencies

```powershell
git clone <repo-url> kubernetes-redis-operator
cd kubernetes-redis-operator

# Tải toàn bộ Go modules theo go.mod / go.sum
go mod download
```

**Giải thích:**
- `go mod download`: chỉ tải module về cache local (`$env:GOPATH\pkg\mod`), **không** sửa `go.mod`. An toàn để chạy nhiều lần. Khác với `go mod tidy` (sẽ thêm/xoá dependency theo source code).

Kiểm tra build OK trước khi tiếp tục:

```powershell
go build ./...
```

- `./...` nghĩa là **toàn bộ package** dưới cwd (đệ quy). Nếu lệnh này fail → fix lỗi compile trước.

---

## 2. Cài controller-gen & generate code

Operator dùng các file **được sinh tự động**:
- `config/crd/bases/*.yaml` — CRD manifest (sinh từ struct trong `api/v1alpha1/`)
- `api/v1alpha1/zz_generated.deepcopy.go` — `DeepCopy()` methods bắt buộc cho mọi K8s API type

Cả hai do `controller-gen` sinh ra. Repo đã có sẵn các file này, nhưng bạn nên regenerate sau khi sửa types.

### 2.1 Cài controller-gen

```powershell
# Tạo thư mục bin local
New-Item -ItemType Directory -Force -Path .\bin | Out-Null

# Set GOBIN trỏ vào .\bin để go install bỏ binary vào đó (không phải $GOPATH\bin global)
$env:GOBIN = "$PWD\bin"

# Cài controller-gen v0.16.4 (version đang dùng trong Makefile)
go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.4
```

**Giải thích:**
- `New-Item -ItemType Directory -Force` = `mkdir -p` của Unix (không lỗi nếu đã tồn tại).
- `Out-Null` = nuốt output của `New-Item` cho gọn.
- `$env:GOBIN = "$PWD\bin"` chỉ set cho **session PowerShell hiện tại**. Mở terminal mới phải set lại.
- `go install <pkg>@<version>` build và copy binary vào `$GOBIN`. Khác `go get` (deprecated cho mục đích này).

Verify:

```powershell
.\bin\controller-gen.exe --version
# → Version: v0.16.4
```

### 2.2 Generate CRDs + RBAC + Webhook manifests

Lệnh tương đương `make manifests`:

```powershell
.\bin\controller-gen.exe `
  rbac:roleName=manager-role `
  crd `
  webhook `
  paths="./..." `
  output:crd:artifacts:config=config/crd/bases `
  output:rbac:artifacts:config=config/rbac
```

**Giải thích các argument:**

| Argument | Tác dụng |
|---|---|
| `rbac:roleName=manager-role` | Sinh `ClusterRole` tên `manager-role` từ marker `+kubebuilder:rbac:...` trong code |
| `crd` | Sinh CRD YAML từ struct `*Spec`/`*Status` |
| `webhook` | Sinh `ValidatingWebhookConfiguration` / `MutatingWebhookConfiguration` |
| `paths="./..."` | Quét toàn bộ package |
| `output:crd:artifacts:config=...` | Output CRD vào `config/crd/bases/` |
| `output:rbac:artifacts:config=...` | Output RBAC vào `config/rbac/` |

> **PowerShell tip:** Backtick `` ` `` ở cuối dòng = line continuation (giống `\` của bash). Đảm bảo **không có khoảng trắng phía sau `` ` ``**.

### 2.3 Generate deepcopy

Lệnh tương đương `make generate`:

```powershell
.\bin\controller-gen.exe `
  object:headerFile="hack/boilerplate.go.txt" `
  paths="./..."
```

- `object` = generator sinh `DeepCopyObject()`, `DeepCopyInto()`, `DeepCopy()` cho mọi struct có marker `+kubebuilder:object:root=true`.
- `headerFile` = prefix license header cho file generated (lấy từ `hack/boilerplate.go.txt`).
- Output: ghi đè `api/v1alpha1/zz_generated.deepcopy.go`.

**Khi nào chạy lại?** Mỗi khi sửa struct trong `api/v1alpha1/*_types.go` (thêm field, đổi marker `+kubebuilder:...`).

### 2.4 Verify

```powershell
go build ./...                               # Phải pass sau khi generate
Get-ChildItem config\crd\bases\              # Phải thấy 3 file CRD
```

---

## 3. Tạo cluster Kubernetes (chọn 1 cách)

### 3.1 Cách A — Dùng Kind (khuyến nghị cho dev local)

```powershell
# Tạo cluster mới tên "redis-op"
kind create cluster --name redis-op
```

**Giải thích:**
- Kind = "Kubernetes IN Docker". Tạo 1 container Docker chạy K8s control plane đầy đủ.
- `--name redis-op` đặt tên context, sau dùng `kubectl config use-context kind-redis-op`.
- Tự động ghi context vào `$env:USERPROFILE\.kube\config`, kubectl trỏ ngay vào cluster mới.

Verify:

```powershell
kubectl cluster-info --context kind-redis-op
kubectl get nodes
```

### 3.2 Cách B — Dùng Minikube

```powershell
minikube start --kubernetes-version=v1.31.0 --driver=docker
```

- `--kubernetes-version` khớp với `ENVTEST_K8S_VERSION = 1.31.0` trong [Makefile](Makefile) — đảm bảo tương thích.

### 3.3 Cách C — Dùng cluster có sẵn

```powershell
kubectl config get-contexts
kubectl config use-context <context-name>
```

---

## 4. Cài CRDs vào cluster

CRD (CustomResourceDefinition) là "schema" cho 3 kind mới: `Redis`, `RedisSentinel`, `RedisCluster`. Phải apply trước khi operator có thể watch chúng.

```powershell
kubectl apply -f config\crd\bases\
```

**Giải thích:**
- `-f <dir>` = apply mọi YAML trong directory.
- Lệnh này tạo 3 CRD: `redis.redis.example.com`, `redisclusters.redis.example.com`, `redissentinels.redis.example.com`.
- Idempotent: chạy lại nhiều lần không hỏng (server-side merge).

Verify:

```powershell
kubectl get crd | Select-String redis.example.com
```

- `Select-String` = `grep` của PowerShell.

---

## 5. Chạy operator

### 5.1 Kịch bản A — Chạy local (out-of-cluster)

Đây là cách **nhanh nhất** để dev: operator chạy như tiến trình bình thường trên máy host.

```powershell
go run .\cmd\main.go
```

**Giải thích:**
- Operator dùng `~/.kube/config` để kết nối API server (qua `ctrl.GetConfigOrDie()` trong [cmd/main.go:62](cmd/main.go)).
- Webhook **mặc định OFF** (`-enable-webhooks=false`) vì chạy local không có cert TLS.
- Log ra stdout, **Ctrl+C** để dừng.

Với flags tuỳ biến:

```powershell
go run .\cmd\main.go `
  --metrics-bind-address=:8080 `
  --health-probe-bind-address=:8081 `
  --leader-elect=false `
  --zap-devel=true
```

| Flag | Mặc định | Ý nghĩa |
|---|---|---|
| `--metrics-bind-address` | `:8080` | Endpoint Prometheus metrics |
| `--health-probe-bind-address` | `:8081` | Endpoint `/healthz`, `/readyz` |
| `--leader-elect` | `false` | Bật khi chạy ≥2 replica để tránh split-brain |
| `--enable-webhooks` | `false` | Bật khi đã setup cert TLS (xem §7) |
| `--zap-devel` | – | Log dạng human-readable |

### 5.2 Kịch bản B — Chạy in-cluster trên Kind

**Bước B1 — Build binary cho Linux (target image distroless):**

```powershell
$env:CGO_ENABLED = "0"
$env:GOOS = "linux"
$env:GOARCH = "amd64"
go build -a -o bin\manager .\cmd\main.go
```

**Giải thích:**
- `CGO_ENABLED=0`: build static binary, không link libc → chạy được trong distroless image.
- `GOOS=linux`: cross-compile cho Linux (vì image base là Linux).
- `-a`: rebuild toàn bộ package (force, bỏ qua cache).

> **Lưu ý:** Sau khi build xong, **reset env vars** để các lệnh `go run` sau không bị cross-compile:
> ```powershell
> Remove-Item Env:CGO_ENABLED, Env:GOOS, Env:GOARCH
> ```

**Bước B2 — Build Docker image:**

```powershell
docker build -t redis-operator:dev .
```

- `-t redis-operator:dev` = tag image (name:tag). `dev` là version tag tự chọn.
- `.` = build context = thư mục hiện tại. Docker đọc `Dockerfile` ở root.
- Dockerfile dùng multi-stage: `golang:1.22` để build (nó tự build lại bên trong, không cần bước B1 ở trên trừ khi muốn build local), `gcr.io/distroless/static:nonroot` chạy runtime.

> **Tip:** Dockerfile đã tự build Go code bên trong (xem [Dockerfile](Dockerfile)), nên thực ra **bước B1 không bắt buộc** nếu bạn chỉ muốn có image. Bước B1 hữu ích khi muốn debug binary ngoài Docker.

**Bước B3 — Load image vào Kind:**

Vì Kind chạy trong Docker container riêng, image trên host **không tự thấy được**:

```powershell
kind load docker-image redis-operator:dev --name redis-op
```

- Lệnh này copy image từ Docker host → container Kind. Bỏ qua bước này → pod sẽ ở trạng thái `ImagePullBackOff`.
- Với Minikube tương đương: `minikube image load redis-operator:dev`.

**Bước B4 — Deploy operator (thủ công, không có kustomize tree):**

> **Gap đã biết của Phase 1A:** Repo hiện **chưa có** `config/manager/` và `config/default/` nên không thể `kustomize build config/default`. Bạn phải tự viết Deployment manifest. Ví dụ tối thiểu:

Tạo file `deploy\operator.yaml`:

```powershell
New-Item -ItemType Directory -Force -Path .\deploy | Out-Null
```

```yaml
# deploy\operator.yaml
apiVersion: v1
kind: Namespace
metadata:
  name: redis-operator-system
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: redis-operator
  namespace: redis-operator-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: redis-operator-manager
subjects:
- kind: ServiceAccount
  name: redis-operator
  namespace: redis-operator-system
roleRef:
  kind: ClusterRole
  name: manager-role        # do controller-gen sinh trong config/rbac/
  apiGroup: rbac.authorization.k8s.io
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis-operator
  namespace: redis-operator-system
spec:
  replicas: 1
  selector:
    matchLabels: { app: redis-operator }
  template:
    metadata:
      labels: { app: redis-operator }
    spec:
      serviceAccountName: redis-operator
      containers:
      - name: manager
        image: redis-operator:dev
        imagePullPolicy: IfNotPresent
        args: ["--leader-elect=false", "--enable-webhooks=false"]
        ports:
        - { containerPort: 8080, name: metrics }
        - { containerPort: 8081, name: health }
        livenessProbe:
          httpGet: { path: /healthz, port: health }
        readinessProbe:
          httpGet: { path: /readyz, port: health }
```

Apply:

```powershell
kubectl apply -f config\rbac\           # ClusterRole do controller-gen sinh
kubectl apply -f deploy\operator.yaml
```

Verify operator đang chạy:

```powershell
kubectl -n redis-operator-system get pods
kubectl -n redis-operator-system logs -l app=redis-operator -f
```

- `-l <selector>` = filter theo label.
- `-f` = follow log realtime.

### 5.3 Kịch bản C — Deploy lên cluster thật (registry)

```powershell
# Login registry trước
docker login ghcr.io

# Build + push
docker build -t ghcr.io/<user>/redis-operator:0.1.0 .
docker push ghcr.io/<user>/redis-operator:0.1.0

# Sửa field image trong deploy\operator.yaml → ghcr.io/<user>/redis-operator:0.1.0
kubectl apply -f deploy\operator.yaml
```

- `IMG` là **fully-qualified** name: `<registry>/<repo>:<tag>`.
- Cluster phải có quyền pull (image public hoặc setup `imagePullSecret`).

---

## 6. Tạo Redis instance đầu tiên

Sau khi operator chạy, tạo 1 CR để test:

```powershell
kubectl apply -f config\samples\redis_standalone.yaml
```

File [config/samples/redis_standalone.yaml](config/samples/redis_standalone.yaml) khai báo Redis 7.2-alpine 5Gi storage có exporter Prometheus.

**Lưu ý:** Sample này tham chiếu `Secret/redis-password` cho auth. Tạo trước nếu không muốn pod CrashLoopBackOff:

```powershell
kubectl create secret generic redis-password `
  --from-literal=password='supersecret123'
```

- `generic` = secret tự do (không phải `tls`/`docker-registry`).
- `--from-literal=key=value` set 1 entry inline.

Theo dõi:

```powershell
kubectl get redis my-redis -o wide
kubectl get all,cm,pvc -l app.kubernetes.io/name=my-redis
kubectl describe redis my-redis
```

| Lệnh | Tác dụng |
|---|---|
| `get all` | Liệt kê pods/svc/deployments/sts (KHÔNG bao gồm CM, PVC, Secret) |
| `,cm,pvc` | Bổ sung ConfigMap & PersistentVolumeClaim vào output |
| `describe` | Show events + status conditions — ưu tiên đọc khi debug |

Test connect:

```powershell
# Port-forward Service ra localhost
kubectl port-forward svc/my-redis 6379:6379

# Terminal khác (cài redis-cli qua choco install redis-64 hoặc dùng container)
redis-cli -a supersecret123 ping       # → PONG
```

Hoặc dùng Docker thay cho `redis-cli` local:

```powershell
docker run --rm -it --network host redis:7.2-alpine `
  redis-cli -h 127.0.0.1 -a supersecret123 ping
```

---

## 7. (Optional) Bật webhook validation

Webhook ngăn user tạo CR sai (vd `RedisCluster.spec.masters < 3`). Mặc định OFF vì cần TLS cert.

### Cách nhanh — dùng cert-manager:

```powershell
kubectl apply -f https://github.com/cert-manager/cert-manager/releases/download/v1.15.3/cert-manager.yaml

# Chờ ready
kubectl -n cert-manager wait --for=condition=available --timeout=120s deployment --all
```

- `wait --for=condition=available` block đến khi Deployment status `Available=True` hoặc timeout.

Sau đó cần thêm `Certificate` + `Issuer` resource trỏ vào webhook Service. Hiện chưa có sẵn trong `config\webhook\` — phải tự viết. Khi đã có cert, chạy operator với `--enable-webhooks=true`.

---

## 8. Cleanup

```powershell
# Xoá CR (trigger finalizer cleanup)
kubectl delete -f config\samples\redis_standalone.yaml

# Xoá operator
kubectl delete -f deploy\operator.yaml
kubectl delete -f config\rbac\

# Xoá CRD (CẢNH BÁO: xoá luôn mọi CR + dữ liệu)
kubectl delete -f config\crd\bases\

# Xoá Kind cluster
kind delete cluster --name redis-op
```

**Lưu ý finalizer:**
- Mỗi CR có finalizer `redis.example.com/finalizer`. Nếu operator **không chạy** lúc delete → CR sẽ bị stuck ở `Terminating` mãi.
- Force remove (chỉ khi đã uninstall operator):
  ```powershell
  kubectl patch redis my-redis -p '{\"metadata\":{\"finalizers\":[]}}' --type=merge
  ```
  > **PowerShell escape:** JSON inline phải escape `"` thành `\"`. Hoặc dùng here-string:
  > ```powershell
  > $patch = @'
  > {"metadata":{"finalizers":[]}}
  > '@
  > kubectl patch redis my-redis -p $patch --type=merge
  > ```

---

## 9. Test

### 9.1 Cài setup-envtest

```powershell
$env:GOBIN = "$PWD\bin"
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.19
```

### 9.2 Download K8s assets (kube-apiserver + etcd binaries)

```powershell
.\bin\setup-envtest.exe use 1.31.0 --bin-dir .\bin\k8s
```

- Tải binary vào `.\bin\k8s\1.31.0-windows-amd64\`.

### 9.3 Chạy test

```powershell
# Lấy path assets và set env var
$env:KUBEBUILDER_ASSETS = & .\bin\setup-envtest.exe use 1.31.0 --bin-dir .\bin\k8s -p path

# Toàn bộ test (loại trừ e2e)
go test (go list ./... | Select-String -NotMatch "/e2e") -coverprofile cover.out
```

```powershell
# Chỉ unit nhanh (không cần envtest)
go test ./api/... ./internal/redis/... -count=1 -v

# Chỉ integration
go test .\internal\controller\ -run TestControllers -v
```

- `-count=1` = bypass test cache, ép chạy lại từ đầu.
- `& <command>` = call operator, chạy executable và capture output.
- Xem coverage HTML:
  ```powershell
  go tool cover -html=cover.out -o cover.html
  Start-Process cover.html
  ```

> Chi tiết test layout xem [README-TESTING.md](README-TESTING.md).

---

## 10. Troubleshooting

| Triệu chứng | Nguyên nhân | Fix |
|---|---|---|
| `make: CreateProcess(NULL, pwd, ...) failed` | Đang chạy `make` trong CMD/PowerShell | Dùng tài liệu này (gọi thẳng `go`/`controller-gen`), hoặc cài Git Bash |
| `controller-gen: not recognized` | Thiếu `.\bin\` prefix hoặc chưa install | Verify `Test-Path .\bin\controller-gen.exe`, install lại §2.1 |
| `no matches for kind "Redis"` | CRD chưa apply | `kubectl apply -f config\crd\bases\` |
| Pod `ImagePullBackOff` (Kind) | Quên `kind load docker-image` | Chạy `kind load docker-image <img> --name <cluster>` |
| Operator không reconcile | Chạy local nhưng kubectl context sai | `kubectl config current-context` → verify |
| CR stuck `Terminating` | Operator down nhưng finalizer còn | Khởi động lại operator, hoặc patch finalizer rỗng (xem §8) |
| `webhook: connection refused` | `--enable-webhooks=true` nhưng chưa có cert | Set `false` hoặc setup cert-manager (§7) |
| Port 8080 bị chiếm | Có service khác (Jenkins…) | `--metrics-bind-address=:9090` |
| `go build` báo missing `DeepCopyObject` | Chưa generate deepcopy | Chạy lại §2.3 |
| Backtick line continuation không hoạt động | Có space sau `` ` `` | Bỏ space, hoặc dùng splat operator/array |

---

## 11. Tóm tắt nhanh — workflow dev hằng ngày

```powershell
# Lần đầu setup (PowerShell ở root repo)
git clone <repo> kubernetes-redis-operator
cd kubernetes-redis-operator
go mod download

$env:GOBIN = "$PWD\bin"
go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.4

.\bin\controller-gen.exe rbac:roleName=manager-role crd webhook `
  paths="./..." `
  output:crd:artifacts:config=config/crd/bases `
  output:rbac:artifacts:config=config/rbac
.\bin\controller-gen.exe object:headerFile="hack/boilerplate.go.txt" paths="./..."

kind create cluster --name redis-op
kubectl apply -f config\crd\bases\

# Vòng lặp dev
go run .\cmd\main.go                              # terminal 1
kubectl apply -f config\samples\redis_standalone.yaml   # terminal 2
kubectl logs -f ...
# Ctrl+C để dừng, sửa code, chạy lại
```

---

## Phụ lục — bảng đối chiếu `make` ↔ PowerShell

| Make target | PowerShell tương đương |
|---|---|
| `make manifests` | `.\bin\controller-gen.exe rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases output:rbac:artifacts:config=config/rbac` |
| `make generate` | `.\bin\controller-gen.exe object:headerFile="hack/boilerplate.go.txt" paths="./..."` |
| `make fmt` | `go fmt ./...` |
| `make vet` | `go vet ./...` |
| `make build` | `go build -o bin\manager.exe .\cmd\main.go` |
| `make run` | `go run .\cmd\main.go` |
| `make docker-build IMG=x` | `docker build -t x .` |
| `make docker-push IMG=x` | `docker push x` |
| `make install` | `kubectl apply -f config\crd\bases\` |
| `make uninstall` | `kubectl delete -f config\crd\bases\` |
| `make deploy IMG=x` | Tự viết `deploy\operator.yaml` rồi `kubectl apply -f` (xem §5.2) |
| `make test` | Xem §9 |
| `make envtest` | `go install sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.19` |
