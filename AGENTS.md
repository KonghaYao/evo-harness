# 评测展示服务：Agent 工作约定

本仓库实现评测系统第 1 层：**Harbor 兼容上传入口 + S3 作业树 + SQLite 分析库 + 读接口**。权威规格见 `docs/eval-display-server-design.md`。本文约束在本仓库内工作的 agent：如何改代码、如何测、什么不许做。

Product docs are written in 书面语; keep new product/design text in the same register. Code comments may be Chinese or English; user-facing error `message` fields should be readable 书面语 or concise English identifiers already used by Harbor/PostgREST.

---

## 模块与布局

Go module 路径为 **`evo-harness`**（不要虚构 `github.com/...`）。HTTP 用 **CloudWeGo Hertz**。包结构必须与设计 §7 一致：

| 路径 | 职责 |
| --- | --- |
| `cmd/eval-display` | 读配置、打开 SQLite / S3、注册路由、优雅退出。禁止在 `main` 里写 SQL 或拼 S3 key。 |
| `internal/evaldisplay/conf` | 环境变量 |
| `internal/evaldisplay/ingest` | 解 `job.tar.gz` / `trial.tar.gz`，校验 Harbor JSON，用量 null→0，finalize 写分析表 |
| `internal/evaldisplay/s3` | AWS SDK v2 Put/Get/Head；MinIO 兼容 endpoint；测试可用 memory / filesystem 假实现 |
| `internal/evaldisplay/store` | `modernc.org/sqlite`（无 CGO）+ WAL；**所有 SQLite 写必须经进程内互斥锁串行化** |
| `internal/evaldisplay/query` | Pass@1、用量汇总、双 job 对照 |
| `internal/evaldisplay/overlay` | 我方 `job-overlay.v1` |
| `internal/evaldisplay/httpapi` | Hertz：Harbor 兼容面 + `/v1` 读/overlay + 后台管理 |
| `internal/evaldisplay/harborcompat` | 换票、PostgREST 薄适配、Storage / TUS |
| `internal/evaldisplay/static` | 前台 `/` 与后台 `/admin/` 静态页 |
| `migrations/` | SQLite DDL |

依赖方向：M6 → M1/M4/M5/harborcompat；M1 → M2+M3；M4/M5 → M3；harborcompat → M2+M3，finalize 调 M1。**M2/M3 不得 import Hertz。** 不用 ORM、Redis、PostgreSQL、pgx、Kafka。

S3 Put 允许并发。SQLite 写（兼容表 + 分析表 + overlay）必须串行（设计 3.7）：`busy_timeout=5000`，WAL，进程内 `sync.Mutex`（或按 `job_id` 分片锁）。v1 单进程，不假设多副本共享同一 sqlite 文件。

---

## 生产 ingest 契约

生产 ingest **只有** Harbor CLI 上传兼容面。操作员设置：

```text
export HARBOR_SUPABASE_URL=http://127.0.0.1:<port>
export HARBOR_SUPABASE_PUBLISHABLE_KEY=<EVAL_DISPLAY_ANON_KEY>
export HARBOR_API_KEY=<EVAL_DISPLAY_TOKEN>
harbor upload <job-dir>
# 或 harbor run … --upload
```

- 实现 `harbor upload` / `harbor run --upload` 所需的换票、PostgREST（`job`/`trial`/`agent`/`model`/`trial_model`）、`rpc/list_my_orgs` 桩、Storage 对象写入、TUS。
- **禁止**实现 `--launch`、`POST /job-submit`、`GET /job-status`、自定义 `POST /v1/jobs` zip/JSON ingest、Hub 分享 / hosted secret。
- 作业树写入 **S3**（键沿用 CLI `objectName`：`jobs/{job_id}/job.tar.gz` 等）。测试可用 memory/fs 或 MinIO，接口须允许替换。
- 列表/聚合标量写入 **SQLite WAL**。`GET /v1/jobs` 只读分析表，且只返回 **finalize 完成**（`hub_job.archive_path` 已设并已写入分析表）的 job。
- 幂等键是 Harbor **job UUID**，不是内容哈希。已存在 job **不得**因内容不同返回 409。

未实现的 `POST /rest/v1/rpc/{name}` 返回 PostgREST 式 `PGRST202` 与 404，以便 CLI 走无 org 遗留路径。推荐实现 `list_my_orgs` 最小桩，不实现 `--share`。

---

## 用量规则（必须遵守）

分析表与列表/详情/聚合/对照接口中，下列字段 **始终出现、不得省略**：

`n_input_tokens`、`n_cache_tokens`、`n_output_tokens`、`cost_usd`、`n_agent_steps`

落库：

1. 源为 null / 缺省 / 空 / JSON `null` → 该列存 **0**。
2. 源为合法数字 → 原样存储（可以为 0）。
3. 五个字段全部属于第 1 类 → `usage_reported=false`，数值全 0。
4. 至少一个字段属于第 2 类 → `usage_reported=true`；仍缺的字段存 0。
5. **禁止**用非零常数、其它 job 均值、`tool_use_events_unverified`、`stream_type_hist`、官方榜数字或 COS 档位名填充。
6. 缺省 0 **不是**测得的零 token / 零成本 / 零步。注释与测试不得把「源未报告」写成「测得 0」。调用方用 `usage_reported` 区分。

`n_agent_steps` 只承认 Harbor `n_agent_steps`（若仅有 `n_steps` 且语义为 agent 步数，可映射）；不得用 peri.txt / overlay 冒充。

对照包 `deepswe-peri-3142` 全部 trial 的 token/cost/steps 为 null：入库后必须为 0 且 `usage_reported=false`。不要为该夹具发明非零 token 或 COS SKU。

---

## 聚合与对照

- Pass@1 = 该 job 已入库 trial 的 `reward` 均值，`reward` ∈ {0,1}。分母是入库 trial 数。
- **禁止混榜：** `peri-3142-full` 与 `peri-3142-fail44-v4flash` 必须是两行，不得合成一条 leaderboard。
- Agent 身份以 `agent_info` 为准，不用对照包里为 null 的 `config.agent.name`。
- Harbor JSON ingest 后只读；我方字段走 overlay（API-18）。CLI 上传不携带 overlay；ingest 插入默认 `attestation_status=unsigned`。

---

## 测试与命令

```bash
gofmt -w .
go test ./...
go run ./cmd/eval-display
```

若 `proxy.golang.org` 不可达，可设 `GOPROXY=https://goproxy.cn,direct`。

- 用量 0 填必须有 **表驱动测试**。
- Pass@1 用 **小型合成 Harbor job**（2–3 个 trial）覆盖「69/113 那种分数口径」，不要把完整 113 题 dump 或 560MB rar 放进 git。
- 至少覆盖：全部用量缺失 → 全 0 且 `usage_reported=false`；以及带用量数字的 job。
- 单元测试中的 S3 用 memory 或 filesystem 假实现，CI **不依赖**真实 AWS。
- 驱动必须是 `modernc.org/sqlite`（`CGO_ENABLED=0` 可编译）。

运行服务（开发）：

```bash
export EVAL_DISPLAY_ADMIN_TOKEN=dev-admin-token
export EVAL_DISPLAY_ANON_KEY=dev-anon
export EVAL_DISPLAY_JWT_SECRET=dev-jwt-secret
# Harbor CLI 换票仍用 EVAL_DISPLAY_TOKEN（可选；仅 ingest，前台不需要）
export EVAL_DISPLAY_TOKEN=dev-token
export EVAL_DISPLAY_SQLITE_PATH=./data/eval-display.sqlite
export EVAL_DISPLAY_S3_DIR=./data/s3
export EVAL_DISPLAY_ADDR=:8080
go run ./cmd/eval-display
```

前台（`/` 与 `GET /v1/jobs*`、`GET /v1/stats`、`GET /v1/compare`）**公开**，无需令牌。后台（`/admin/` 静态页可匿名；`PUT /v1/jobs/{id}/overlay`、`GET/DELETE /v1/admin/*`）需要 `Authorization: Bearer $EVAL_DISPLAY_ADMIN_TOKEN`。Harbor ingest 密钥或其它非后台 Bearer 调用后台接口返回 **401/403**。Harbor 面：请求头 `apikey` = `EVAL_DISPLAY_ANON_KEY`，`HARBOR_API_KEY` = `EVAL_DISPLAY_TOKEN`，先 `POST /functions/v1/api-key-exchange` 换 JWT。`/healthz`、`/readyz` 可匿名。

---

## Docker 与 CI

镜像监听 `EVAL_DISPLAY_ADDR=:8080`（全接口），前台读接口公开，**不要**为 viewer 配置令牌。SQLite 与文件系统作业树默认写在 `/data`。

```bash
docker compose up --build
# 可选 MinIO（需同时把作业树切到 aws/MinIO）：
# make compose-minio
# docker.io 不可达时：docker build --build-arg BUILDER_IMAGE=... --build-arg GOPROXY=https://goproxy.cn,direct
```

`docker compose up` 使用开发默认 `EVAL_DISPLAY_ADMIN_TOKEN` / `EVAL_DISPLAY_ANON_KEY` / `EVAL_DISPLAY_JWT_SECRET`（与上文一致）。生产必须覆盖这三项；Harbor 上传另设 `EVAL_DISPLAY_TOKEN`。

GitHub Actions：

- `.github/workflows/ci.yml`：push / PR 上 `go test ./...`、`go vet ./...`（`CGO_ENABLED=0`）、`docker build`、`docker compose config`。
- `.github/workflows/docker.yml`：仅 `main`/`master`/tag 推送到 `ghcr.io/<owner>/<repo>`（`GITHUB_TOKEN` + `packages: write`）。fork PR 不推送；无额外云凭证。

---

## 做 / 不做

**做**

- 按设计实现 Harbor 上传兼容面 API-01…API-09 与展示面 API-10…API-18。
- job finalize（`hub_job.archive_path` 从空变为非空）后从 S3 解包，抽标量入 SQLite；失败则分析表不落、`archive_path` 保持空，便于 CLI 重试。
- 鉴权分三套：Harbor JWT + anon key（ingest）；前台读接口公开；后台 `EVAL_DISPLAY_ADMIN_TOKEN`。`/healthz`、`/readyz` 与前台静态页可匿名。

**不做**

- peri.txt / ATIF chat 查看器；不把 peri.txt 写入 SQLite。
- 发明 COS SKU、GPU/引擎消耗、非零用量去填对照包。
- 把 peri-3142-full 与 fail44 合成一行榜。
- Kafka / Redis / PostgreSQL / 自定义 `POST /v1/jobs` / `--launch`。
- 把 560MB rar 解压进 git；提交密钥、`.env`、真实凭据。
- 把草稿 HTML（`docs/eval-panel-draft.html`）里的示意 cost/token/COS/chat 当作真值或测试期望。
- 为了让测试好写而缩小产品契约（例如省略用量字段、把缺省 0 当成测得 0）。
