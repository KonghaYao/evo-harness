# 评测展示服务

本仓库实现评测系统第 1 层：**Harbor 兼容上传入口 + S3 作业树 + SQLite 分析库 + 读接口**。权威规格见 [`docs/eval-display-server-design.md`](docs/eval-display-server-design.md)。

生产 ingest **只有** Harbor CLI 上传兼容面（`harbor upload` / `harbor run … --upload`）。本服务不实现 hosted 拉起（`--launch`、`POST /job-submit`），也不提供自定义 `POST /v1/jobs`。

兼容面已按 Harbor CLI **0.23.0** 核实。

## 运行服务

前台（`/` 与 `GET /v1/jobs*`、`GET /v1/stats`、`GET /v1/compare`）公开，无需令牌。`/healthz`、`/readyz` 与后台静态页 `/admin/` 可匿名访问。写入 overlay 与 `/v1/admin/*` 须使用 `Authorization: Bearer $EVAL_DISPLAY_ADMIN_TOKEN`。

### 本地 `go run`（文件系统对象存储）

```bash
export EVAL_DISPLAY_ADMIN_TOKEN=dev-admin-token
export EVAL_DISPLAY_ANON_KEY=dev-anon
export EVAL_DISPLAY_JWT_SECRET=dev-jwt-secret
# Harbor CLI 换票用；仅 ingest，前台不需要
export EVAL_DISPLAY_TOKEN=dev-token
export EVAL_DISPLAY_SQLITE_PATH=./data/eval-display.sqlite
export EVAL_DISPLAY_S3_DIR=./data/s3
export EVAL_DISPLAY_ADDR=:8080
go run ./cmd/eval-display
```

`proxy.golang.org` 不可达时可设 `GOPROXY=https://goproxy.cn,direct`。

### Docker Compose（RustFS）

作业树默认写入 Compose 中的 **RustFS**（S3 API `:9000`，控制台 `:9001`）。不要改用 MinIO。

```bash
docker compose up --build
```

镜像监听 `EVAL_DISPLAY_ADDR=:8080`（宿主机映射 `8080:8080`）。Compose 开发默认值与上文 `go run` 一致（`EVAL_DISPLAY_ADMIN_TOKEN` / `EVAL_DISPLAY_ANON_KEY` / `EVAL_DISPLAY_JWT_SECRET` / `EVAL_DISPLAY_TOKEN`），以及本地 RustFS 访问密钥 `EVAL_DISPLAY_S3_ACCESS_KEY` / `EVAL_DISPLAY_S3_SECRET_KEY`（默认 `rustfs-dev` / `rustfs-dev-secret`，勿用于生产）。

生产必须覆盖 ADMIN / ANON / JWT 与对象存储凭据；Harbor 上传另设 `EVAL_DISPLAY_TOKEN`。

`docker.io` 不可达时：

```bash
docker build --build-arg BUILDER_IMAGE=... --build-arg GOPROXY=https://goproxy.cn,direct
```

## Harbor CLI 上传

服务启动后，将官方 CLI 指向本进程即可入库。CLI **没有** `--upload-url`；上传目标就是 `HARBOR_SUPABASE_URL`。

### 安装 CLI

Harbor 要求 **Python ≥ 3.12**。官方推荐用 [uv](https://docs.harborframework.com/getting-started/installation)：

```bash
uv tool install harbor
harbor --version
```

亦可用 `pip install harbor`。指向本服务时无需对官方 Hub 执行 `harbor auth login`；换票使用下方 `HARBOR_API_KEY`。

### 环境变量对照

| Harbor CLI | 本服务 |
| --- | --- |
| `HARBOR_SUPABASE_URL` | 本服务 origin（例如 `http://127.0.0.1:8080`） |
| `HARBOR_SUPABASE_PUBLISHABLE_KEY` | `EVAL_DISPLAY_ANON_KEY` |
| `HARBOR_API_KEY` | `EVAL_DISPLAY_TOKEN` |

`http://` **仅**允许 loopback：`127.0.0.1` 或 `localhost`（含 `::1`）。不要把 CLI 指到 `0.0.0.0`；服务端 `EVAL_DISPLAY_ADDR=:8080` 可以监听所有网卡，但 CLI 的 URL 必须是本机可访问的 loopback 主机名。非 loopback 须用 `https://`。

### 作业目录

`harbor upload` 的参数是已完成作业的本地目录，须为 Harbor 布局（[官方说明](https://docs.harborframework.com/core-concepts/harbor-hub/upload)：目录必须含 `config.json` 与 `result.json`）。CLI 将含 `result.json` 的**直接子目录**视为 trial，例如：

```text
<job-dir>/
  config.json
  result.json
  lock.json          # 有则随 job 归档上传
  <trial-name>/
    config.json
    result.json
    lock.json        # 每个 trial 必有，否则 CLI 在本地打包失败
```

trial 的 `lock.json` 是客户端约束：缺失时请求到不了本服务。本服务不代为编造 lock。

### 上传

```bash
export HARBOR_SUPABASE_URL=http://127.0.0.1:8080
export HARBOR_SUPABASE_PUBLISHABLE_KEY=dev-anon
export HARBOR_API_KEY=dev-token
harbor upload <job-dir>
# 或 harbor run … --upload
```

`PUBLISHABLE_KEY` / `API_KEY` 须与当时进程的 `EVAL_DISPLAY_ANON_KEY` / `EVAL_DISPLAY_TOKEN` 一致（上例对应开发默认值）。上传中断后可再执行 `harbor upload <job-dir>`；幂等键是 Harbor **job UUID**。

job finalize（兼容表 `hub_job.archive_path` 从空变为非空）后，本服务从对象存储解包并写入分析表。`GET /v1/jobs` 只返回 finalize 完成的作业。

### 查看与 overlay

- 前台：浏览器打开 `http://127.0.0.1:8080/`（公开）。
- 后台 overlay：`/admin/` 静态页可匿名打开；保存 overlay 与管理接口需要 `EVAL_DISPLAY_ADMIN_TOKEN`。

### 本服务不提供的路径

不要对展示服务使用下列方式入库或拉起评测：

- `harbor run --launch`
- `POST /job-submit`、`GET /job-status`
- 自定义 `POST /v1/jobs`

`--share` / Hub 分享亦未实现。

## 测试与 CI

```bash
gofmt -w .
go test ./...
```

GitHub Actions：push / PR 上 `go test ./...`、`go vet ./...`（`CGO_ENABLED=0`）、`docker build`、`docker compose config`（[`.github/workflows/ci.yml`](.github/workflows/ci.yml)）。
