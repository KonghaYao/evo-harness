# 评测展示服务设计（Harbor 上传兼容 + S3 + SQLite + 读模型）

本文只设计评测系统第 1 层：**评测展示服务 = Harbor 兼容上传入口 + 对象存储 + 分析库 + 读接口（及可选静态前端托管）**。v1 共 **7** 个模块、**18** 个对外 HTTP 路由（Harbor 上传兼容 **9** + 展示读/overlay **9**）。实现语言 Go，HTTP 框架 Hertz；作业树落 **S3**（本地 Compose 默认 RustFS；单元测试用 memory/fs）；列表/聚合标量落 **SQLite**（WAL）。评测环境（Harbor；本仓库对照包实际由 Pier 产出 Harbor 目录布局）跑完作业后，用官方 CLI `harbor upload` 或 `harbor run … --upload` 指向本服务；本服务是 **Harbor 兼容 ingest + 我方读模型**，不是第二套 runner，不调用 `harbor run`，不实现 Harbor Hub UI，也不实现 hosted 拉起（`--launch` / `POST /job-submit`）。Harness / Model / 评测集在 Harbor 侧可替换；本服务原样存储作业里记录的身份字段，缺则按本文规则填默认值，不编造非零用量、不编造 COS 档位名。

生产 ingest **只有** Harbor 上传兼容面。已删除自定义 `POST /v1/jobs` zip/JSON 作为对外契约。夹具测试可在进程内调用 ingest 包，不经过该已废路径。

产品背景（不展开实现）：接到 AOS 后质量问题难以归因、资源预估缺少测试标准、COS 优化目标与 AOS 引擎质量背书。本服务先保证「有 job/trial id 的结果可查、Pass@1 可复算、用量字段始终出现」。GPU × 引擎消耗、COS 档位目录、AOS 编排路径若 Harbor 记录与 overlay 中不存在，v1 不推断。

对照包 `deepswe-peri-3142`（Pier 0.3.1 产出的 Harbor 形目录）可作为 ingest 验收夹具，不可当作 Pier 官方 DeepSWE 榜或产品 SLA。该包实测聚合（由 trial `verifier_result.rewards.reward` 的 0/1 导出，非 Hub 自动打分）：`jobs/peri-3142-full`，`job_id=b7757551-4f4f-45d2-9580-2e30aa8a6077`，69/113；`jobs/peri-3142-fail44-v4flash`，`job_id=5d8e2b04-4c8c-4506-940e-20543dd18970`，20/44。缺口与 overlay 见 `docs/eval-data-gaps-deepswe-peri-3142.md`、`fixtures/deepswe-peri-3142/our-overlay.json`。`docs/eval-panel-draft.html` 仅作页面分区参考；草稿中的 COS 档名、成本、token、步数、ATIF 会话均为示意，v1 不得当作字段真值或必须实现 chat。

---

## 范围

**之内：** 实现 Harbor CLI 上传所依赖的 **薄兼容面**（见第 4 节：已核实事实 vs 假定），使 `HARBOR_SUPABASE_URL` 指向本服务后 `harbor upload <job-dir>` / `harbor run --upload` 能完成；将不可变作业树写入 S3；从 Harbor JSON 抽出标量写入 SQLite；按 Harbor `job_id` 幂等（与官方 CLI 语义对齐，见 3.6）；提供内部 token 下的 job/trial 列表与详情；按 reward 聚合 Pass@1；用量字段（token / cost / 步数）在分析表与列表/详情/聚合接口中始终出现；我方 overlay 与 Harbor JSON 分权。

**之外：** 运行 Harbor / Pier；PeriAgent adapter；用 `peri.txt` 或 ATIF 做会话回放（`peri.txt` 可随 job 树进入 S3，但不提供 chat API）；AOS 控制台与编排评测；COS 引擎遥测与档位目录；对外官方 DeepSWE 排名；Harbor Hub 产品目录（hosted 提交、secret、leaderboard、分享 RPC、Hub 下载 CLI、Hub UI）；Kafka / Redis / Kubernetes（v1 规模不需要）。

规模假设：内网，数十个 job，每 job 约 \(10^2\)–\(10^3\) trial。SQLite 足够承担分析查询。优化目标是 ingest 正确、列表/详情延迟可接受，不为公网 C10k 或幻想 QPS 加组件。

---

## 1. 目标与非目标

### 1.1 目标

1. 把一次已完成（或 `--upload` 流式进行中、最终 finalize 完成）的 Harbor 作业收成不可变的 trial 收据：每行能指回 `TrialResult.id` 与 `job_id`。
2. 在本服务内聚合 Pass@1（Hub 不会从 trials 自动出榜；本服务也不依赖 Hub 打分）。
3. 列表、详情、聚合接口始终返回 `n_input_tokens`、`n_cache_tokens`、`n_output_tokens`、`cost_usd`、`n_agent_steps`。源为 null / 缺省 / 空时**持久化为 0**，不省略字段。
4. 允许无 `hub_org`、无 Hub dataset ref 的本地/离线作业入库；默认 `attestation.status=unsigned`。
5. Harbor 原生 JSON 与我方 overlay 分权：前者 ingest 后只读，后者可后补。
6. 操作员可将官方 Harbor CLI 指向本服务完成上传，无需自研第二套打包客户端。

### 1.2 非目标

1. 调用 `harbor run`、调度沙箱、拉数据集。不实现 hosted rollout：`harbor run --launch` 与 `POST /job-submit` **不是**本服务 ingest。官方文档写明 `--launch` 与 `--upload` **互斥**。
2. PeriAgent、ATIF `trajectory.json` 会话面板。`agent/peri.txt` 是 Peri stream-json，不是 ATIF。v1 不把 peri.txt 写入 SQLite、不提供 chat 接口。
3. AOS 控制台、租户路由、把 AOS 进程本身做成评测对象。
4. COS SKU / 档位目录、GPU 资源估算 API、引擎质量背书文案。overlay 只存作业里已有的 `endpoint_class`（对照包为 `cloud`）等我方字段。
5. 把对照包或本服务聚合结果标成 Pier 官方 DeepSWE 榜。
6. 复刻 Harbor Hub：不实现 org 分享、用户分享、Hub viewer、`harbor job download` / `harbor trial download` 作为产品面（Storage GET 仅在服务内部解包时使用）。

---

## 2. 模块划分

共 **7** 个模块。Harbor 作业 JSON 的解析与必填校验属 M1；对象存储属 M2；SQLite（兼容表 + 分析表）属 M3；Pass@1 与用量汇总属 M4；我方字段属 M5；HTTP（兼容面 + `/v1` 读）属 M6；静态页属 M7（可空实现）。

| 编号 | 包名（见第 7 节） | 职责 | 不负责 |
| --- | --- | --- | --- |
| M1 ingest | `internal/evaldisplay/ingest` | 解 `job.tar.gz` / `trial.tar.gz`；校验 Harbor job/trial；null 用量→0；在 job finalize 后组装分析表写入批次 | 跑评测；改写 `result.json` 语义；把 peri.txt 当 ATIF；实现 PostgREST 语法 |
| M2 s3 | `internal/evaldisplay/s3` | AWS SDK v2：Put/Get/Head；S3 兼容 endpoint（RustFS）；SSE 可选 | SQL；HTTP 路由 |
| M3 store | `internal/evaldisplay/store` | SQLite（`database/sql` + sqlc，engine=sqlite）；WAL；写锁；`job_id` 唯一 | 聚合口径；S3 传输 |
| M4 query | `internal/evaldisplay/query` | Pass@1、n、时长、token/cost/steps 汇总；双 job 对照 | ingest 校验；编造非零用量 |
| M5 overlay | `internal/evaldisplay/overlay` | 我方 `job-overlay.v1`：runner、sandbox、endpoint_class、job_type、attestation、dataset_*、incomparability | Harbor `TrialResult` 字段所有权 |
| M6 httpapi | `internal/evaldisplay/httpapi` | Hertz 路由：Harbor 兼容面 + `/v1` 读/overlay；鉴权；错误体；请求体上限 | SQL；聚合公式（调用 M4）；S3 客户端细节 |
| M7 static | `internal/evaldisplay/static` | 可选：托管日后静态文件（`GET /` 下的 html/js/css） | 大型 SPA 框架；chat 面板 |

v1 **API 优先**。M7 可以只注册占位或暂不挂路由；前端可后做。不要在本期引入 React/Vue 工程。Harbor 兼容的 PostgREST 适配可放在 `internal/evaldisplay/harborcompat`，由 M6 注册，**不单列模块号**。

```
Harbor CLI
  harbor upload | harbor run --upload
  HARBOR_SUPABASE_URL = 本服务
        │  换票 + PostgREST + Storage（tar.gz，不是自定义 zip API）
        ▼
   ┌──────────┐     对象键（CLI objectName）      ┌────────┐
   │ M6 httpapi│ ───────────────────────────────► │ M2 s3  │
   └────┬─────┘                                    └────────┘
        │ hub_* 行（job/trial 元数据、archive_path）
        ▼
   ┌──────────┐
   │ M3 store │  SQLite（WAL）
   └────▲─────┘
        │ job finalize：解包 → 抽标量
   ┌──────────┐     Harbor JSON 只读分析列         │
   │ M1 ingest│ ───────────────────────────────────┘
   └────┬─────┘
        │ overlay JSON
        ▼
   ┌──────────┐
   │ M5 overlay│ ──────────────────────────────────► M3
   └──────────┘
        ▲
        │ PUT overlay
   ┌──────────┐     读 job/trial/聚合               ┌──────────┐
   │ M6 httpapi│ ◄──────────────────────────────── │ M4 query │
   └────┬─────┘                                    └──────────┘
        │ 可选
        ▼
   ┌──────────┐
   │ M7 static│  静态文件，无业务逻辑
   └──────────┘
```

**字段所有权**

- Harbor 侧（M1 在 finalize 后写入分析表 `jobs.*` / `trials.*` 抽出列；完整 `config.json` / `result.json` / `agent/` / `verifier/` 在 S3）：`id`、`job_id`、`task_name`、`task_checksum`、`agent_info`、`verifier_result.rewards`、四阶段 `TimingInfo`、`exception_info`、`agent_result` 用量。`config.agent.name` 在对照包中全为 null，**身份以 `agent_info` 为准**。
- 我方侧（M5，`job_overlays`）：`runner`（pier/harbor + version）、`sandbox`、`endpoint_class`、`job_type`（可空）、`attestation`、`dataset_path` / `dataset_ref`（可空）、`hub_org`（可空）、`harbor_version`（可空）、`incomparability`。对照包 overlay 已填 Pier 0.3.1、`endpoint_class=cloud`、`job_type=null`、`attestation.unsigned`、不可比说明。Harbor CLI 上传 **不携带** overlay；ingest 插入默认 unsigned 行，后补走 API-18。
- 本服务派生（M4，可在 ingest 时物化到 `jobs` 以免列表扫 trial）：`pass_at_1`、`n_reward_1`、job 级用量合计、`usage_reported`。

不引入消息队列。单进程 Hertz；CLI 默认 10 路并发上传 trial，HTTP 可并发进入，**SQLite 写在进程内串行化**（见 3.7）。

---

## 3. 数据模型

分析查询只走 SQLite。不可变作业树（含可能很大的 `agent/`、`verifier/`、`peri.txt`、完整 `result.json`）走 S3。作业在 **job finalize 成功**（兼容表 `hub_job.archive_path` 非空且分析表已写入）之后，Harbor 数据不可变；只允许改 overlay。不建第二套「Harbor 任务表」来表达数据集或 agent 目录（兼容表 `hub_agent` / `hub_model` 仅服务 CLI upsert，不是产品目录）。

用量列均为 `NOT NULL DEFAULT 0`，接口同样始终输出数字。另存 `usage_reported`：源中该 trial/job 至少有一个用量字段为非 null 时为 true，否则 false。数值仍为 0 时，调用方用该布尔值区分「源未报告」与「源报告了 0」；**不得把缺省 0 写成 null，也不得把缺省 0 当成测得的零 token。**

`trajectory_uri` 预留可空，v1 ingest **不**根据 peri.txt 填写，不提供读轨迹 API。若 CLI 上传了 `trials/{trial_id}/trajectory.json`，对象可留在 S3，分析表该列仍为 null。

### 3.1 S3

- **生产：** AWS S3，SDK v2（`github.com/aws/aws-sdk-go-v2`）。
- **测试：** 单元测试用 memory/fs；本地联调用 RustFS 或 LocalStack，配置 `EVAL_DISPLAY_S3_ENDPOINT` + path-style。
- **桶：** `EVAL_DISPLAY_S3_BUCKET`。可选 SSE：`EVAL_DISPLAY_S3_SSE`（空 = 关闭；`AES256` 或 `aws:kms`）。
- **不要求** Kafka、CDN、多桶。

Harbor CLI 写入 Storage bucket 名恒为 `results`，`objectName` 由官方 `Uploader` 给定（源码已核实）：

| CLI objectName | Content-Type（建议） | 说明 |
| --- | --- | --- |
| `jobs/{job_id}/job.tar.gz` | `application/gzip` | job 级 allowlist 归档 |
| `jobs/{job_id}/job.log` | `text/plain` | 可选 |
| `trials/{trial_id}/trial.tar.gz` | `application/gzip` | trial 级 allowlist 归档（含 `agent/`，因此可含 peri.txt） |
| `trials/{trial_id}/trajectory.json` | `application/json` | 可选；ATIF 时才有。对照包无 |

本服务把上述键落到 **同一 S3 桶**，不改 CLI 的 objectName。另在 finalize 时展开一棵只读树，供详情 `include=config,result` 与排障，**列表不 ListObjects 扫桶**：

```
s3://{bucket}/jobs/{job_id}/job.tar.gz
s3://{bucket}/jobs/{job_id}/job.log
s3://{bucket}/trials/{trial_id}/trial.tar.gz
s3://{bucket}/jobs/{job_id}/files/config.json
s3://{bucket}/jobs/{job_id}/files/result.json
s3://{bucket}/jobs/{job_id}/files/lock.json          # 有则存
s3://{bucket}/jobs/{job_id}/files/{trial_name}/...   # 镜像 Harbor jobs/<name>/<trial>/
```

`files/` 的根对应 Harbor 本地 `jobs/<job_name>/` 去掉目录名后的内容（官方 job tar 的 arcname 带 `{job_dir.name}/` 前缀，展开时剥掉该前缀，统一挂到 `files/`）。`job_name` 进 SQLite，不进 S3 主键。

大日志与 peri.txt **只**在 `files/` 与 tar.gz 中，不进 SQLite。

### 3.2 SQLite 分析表 `jobs`

单文件路径：`EVAL_DISPLAY_SQLITE_PATH`，默认 `./data/eval-display.sqlite`。`PRAGMA journal_mode=WAL`；`PRAGMA busy_timeout=5000`；`PRAGMA foreign_keys=ON`。

SQLite 无 UUID / JSONB / TIMESTAMPTZ：UUID 与时间戳用 `TEXT`（时间戳存 UTC RFC3339）；JSON 小对象用 `TEXT`；布尔用 `INTEGER` 0/1。

| 列 | 类型 | 约束 | 来源 |
| --- | --- | --- | --- |
| `job_id` | `TEXT` | PK | Harbor job `id` / trial 上的 `job_id` |
| `job_name` | `TEXT` | `NOT NULL` | 目录名或 Hub `job_name`，如 `peri-3142-full` |
| `s3_prefix` | `TEXT` | `NOT NULL` | `jobs/{job_id}/` |
| `started_at` | `TEXT` | 可空 | job 或 trial 最小 `started_at` |
| `finished_at` | `TEXT` | 可空 | job 或 trial 最大 `finished_at` |
| `n_trials` | `INTEGER` | `NOT NULL` | 入库 trial 数 |
| `n_errors` | `INTEGER` | `NOT NULL DEFAULT 0` | job result 若有则用，否则 0 |
| `n_retries` | `INTEGER` | `NOT NULL DEFAULT 0` | 同上 |
| `n_reward_1` | `INTEGER` | `NOT NULL` | `reward=1` 条数 |
| `pass_at_1` | `REAL` | `NOT NULL` | `n_reward_1 / n_trials` |
| `n_input_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 见 3.6 |
| `n_cache_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 见 3.6 |
| `n_output_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 见 3.6 |
| `cost_usd` | `REAL` | `NOT NULL DEFAULT 0` | 见 3.6 |
| `n_agent_steps` | `INTEGER` | `NOT NULL DEFAULT 0` | 见 3.6 |
| `usage_reported` | `INTEGER` | `NOT NULL DEFAULT 0` | 见 3.6 |
| `agent_name` | `TEXT` | `NOT NULL DEFAULT ''` | finalize 时按 trial `agent_info` 多数身份写入；列表与 `agent_name` 过滤只读此列 |
| `agent_version` | `TEXT` | 可空 | 同上，多数 `agent_info.version` |
| `model_name` | `TEXT` | 可空 | 同上，多数 `model_info.name` |
| `model_provider` | `TEXT` | 可空 | 同上，多数 `model_info.provider` |
| `ingest_sha256` | `TEXT` | 可空 | finalize 时对展开后 job+trial 的 `result.json` 规范化摘要，仅日志/排障；**不**作为与 Harbor CLI 冲突的 409 条件 |
| `ingested_at` | `TEXT` | `NOT NULL` | 本服务，finalize 写入分析表的时刻 |

索引：`jobs_ingested_at_idx (ingested_at DESC)`；`jobs_job_name_idx (job_name)`；`jobs_agent_name_idx (agent_name)`；`jobs_model_name_idx (model_name)`。

`GET /v1/jobs` **不得**为每个 job 再扫 `trials` 求多数身份；详情列表字段与过滤均用上表四列。旧库启动时 ALTER 补列，并按已入库 trial 回填空的 `agent_name`。

不要求 `hub_org`。对照包无此字段，仍须能 ingest。

**不**在分析表保存完整 `config.json` / `result.json`。详情接口需要原文时按 `s3_prefix` 读 `files/config.json`、`files/result.json`。

### 3.3 SQLite 分析表 `trials`

| 列 | 类型 | 约束 | 来源 |
| --- | --- | --- | --- |
| `trial_id` | `TEXT` | PK | `TrialResult.id` |
| `job_id` | `TEXT` | `NOT NULL REFERENCES jobs ON DELETE CASCADE` | `TrialResult.job_id`，须等于外层 job |
| `task_name` | `TEXT` | `NOT NULL` | Harbor |
| `task_checksum` | `TEXT` | `NOT NULL` | Harbor；缺则该 trial 不进分析表（finalize 整单失败，见 4.3） |
| `trial_name` | `TEXT` | 可空 | 有则存 |
| `task_id` | `TEXT` | 可空 | 有则存 |
| `source` | `TEXT` | 可空 | 有则存；对照包无 Hub dataset 名 |
| `trial_uri` | `TEXT` | 可空 | 有则存 |
| `agent_name` | `TEXT` | `NOT NULL` | `agent_info.name`；无则拒收（不用 null 的 `config.agent.name`） |
| `agent_version` | `TEXT` | 可空 | `agent_info.version` |
| `model_name` | `TEXT` | 可空 | `agent_info.model_info.name` 或 config `model_name` |
| `model_provider` | `TEXT` | 可空 | `agent_info.model_info.provider` |
| `agent_info` | `TEXT` | `NOT NULL` | Harbor 原文 JSON，体积小 |
| `reward` | `REAL` | `NOT NULL` | `verifier_result.rewards.reward`，仅 0 或 1 |
| `f2p` | `REAL` | 可空 | `rewards.f2p` 有则抽出 |
| `p2p` | `REAL` | 可空 | `rewards.p2p` 有则抽出 |
| `verifier_rewards` | `TEXT` | `NOT NULL` | `rewards` 原文 JSON |
| `exception_type` | `TEXT` | 可空 | `exception_info` |
| `exception_info` | `TEXT` | 可空 | JSON；对照包全 null，允许 |
| `started_at` / `finished_at` | `TEXT` | 可空 | Harbor |
| `environment_setup` | `TEXT` | 可空 | TimingInfo JSON |
| `agent_setup` | `TEXT` | 可空 | TimingInfo JSON |
| `agent_execution` | `TEXT` | 可空 | TimingInfo JSON |
| `verifier_timing` | `TEXT` | 可空 | TimingInfo JSON（字段名 `verifier`） |
| `n_input_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | `agent_result`，缺→0 |
| `n_cache_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 缺→0 |
| `n_output_tokens` | `INTEGER` | `NOT NULL DEFAULT 0` | 缺→0 |
| `cost_usd` | `REAL` | `NOT NULL DEFAULT 0` | 缺→0 |
| `n_agent_steps` | `INTEGER` | `NOT NULL DEFAULT 0` | 缺→0；**禁止**用 `stream_type_hist` / overlay 的 `tool_use_events_unverified` 冒充 |
| `usage_reported` | `INTEGER` | `NOT NULL DEFAULT 0` | 见 3.6 |
| `trajectory_uri` | `TEXT` | 可空 | v1 保持 null |
| `s3_trial_prefix` | `TEXT` | 可空 | `jobs/{job_id}/files/{trial_name}/` |

索引：`trials_job_id_idx (job_id)`；`trials_job_reward_idx (job_id, reward)`；`trials_job_checksum_idx (job_id, task_checksum)`。

### 3.4 `job_overlays`

与分析表 `jobs` 1:1，按 `job_id`。

| 列 | 类型 | 约束 | 说明 |
| --- | --- | --- | --- |
| `job_id` | `TEXT` | PK, FK | |
| `runner_name` | `TEXT` | 可空 | `pier` / `harbor` |
| `runner_version` | `TEXT` | 可空 | 对照包 `0.3.1`（来自 lock.json，不是 harbor 包版本） |
| `sandbox_type` | `TEXT` | 可空 | 如 `docker` |
| `sandbox_location` | `TEXT` | 可空 | 如 `local` |
| `endpoint_class` | `TEXT` | 可空 | 对照包 `cloud`；不是 COS SKU |
| `job_type` | `TEXT` | 可空，`CHECK (job_type IN ('H','M'))` | 对照包为 null（单点基线，不是 H/M 对照） |
| `job_type_reason` | `TEXT` | 可空 | |
| `attestation_status` | `TEXT` | `NOT NULL DEFAULT 'unsigned'` | v1 取值 `unsigned`；已签预留 `signed` |
| `attestor` | `TEXT` | 可空 | |
| `hub_org` | `TEXT` | 可空 | **v1 不作为 ingest 必填** |
| `harbor_version` | `TEXT` | 可空 | 对照包无 |
| `dataset_name` / `dataset_version` / `dataset_ref` | `TEXT` | 可空 | 对照包无 Hub ref；`task_name` 的 `datacurve/` 前缀不得当作 ref |
| `dataset_path` | `TEXT` | 可空 | 对照包为本地 path |
| `incomparability` | `TEXT` | 可空 | 对照包：非 Pier 官方 DeepSWE 协议 |
| `extra` | `TEXT` | `NOT NULL DEFAULT '{}'` | JSON；如 `fail44_equals_full_reward0`，不进聚合公式 |

无 overlay 部分时仍插入一行：`attestation_status='unsigned'`，其余 null。

### 3.5 Harbor 兼容表（仅服务 CLI，不作为展示读模型）

官方 CLI 在 finalize 前用 `archive_path IS NULL` 判断未完成。这些表镜像 Uploader 用到的 Hub 行，**不是** Pass@1 来源。

| 表 | 对应 Hub | 最低列（与源码 insert/select 对齐） |
| --- | --- | --- |
| `hub_job` | `job` | `id`（PK）、`job_name`、`config`（TEXT JSON）、`visibility`、`started_at`、`finished_at`、`archive_path`、`log_path`、`n_planned_trials`、`org_id`（可空） |
| `hub_trial` | `trial` | `id`（PK）、`job_id`、`trial_name`、`task_name`、`task_content_hash`、`lock`、`agent_id`、`config`、`rewards`、`exception_type`、四阶段时间戳、`archive_path`、`trajectory_path` |
| `hub_agent` | `agent` | `id`、`name`、`version`；冲突键按 CLI：`added_by,name,version`。v1 单租户时 `added_by` 固定为换票 JWT 的 `sub` |
| `hub_model` | `model` | `id`、`name`、`provider`；冲突键 `added_by,name,provider` |
| `hub_trial_model` | `trial_model` | `trial_id`、`model_id`、token/cost 可空列 |

Hub 生成类型里 `job` 还有 `created_by`、`is_hosted` 等列。CLI insert 不一定带全。缺列时：`created_by`←JWT `sub`；`is_hosted`←false。其余未出现的列不要为了「像 Hub」而造。

`GET /v1/jobs` **只**读分析表 `jobs`，且只返回 finalize 已完成的行。流式 `--upload` 中尚未 finalize 的 job 对展示面不可见。

### 3.6 Harbor 字段映射与用量落库

| Harbor | 本服务 |
| --- | --- |
| job / trial `config.json`、`result.json` | S3 `files/`；分析表只抽标量 |
| `TrialResult.id` | `trials.trial_id` |
| `TrialResult.job_id` | `trials.job_id` / `jobs.job_id` |
| `task_name`、`task_checksum`、`agent_info` | 同名列 |
| `verifier_result.rewards` | `verifier_rewards` + 抽出 `reward`/`f2p`/`p2p` |
| `exception_info` | `exception_info` |
| 四阶段 TimingInfo | 四列 TEXT JSON |
| `agent_result.n_input_tokens` 等 | 抽出列，null→0 |
| `agent/trajectory.json` | 不作为 v1 必填；无则 `trajectory_uri` 空 |
| `hub_org`、`harbor_version`、dataset Hub ref、job_type、COS sku、GPU/engine、`verifier_environment_mode` | 不编造；能进 overlay 的进 overlay，否则空 |
| `n_agent_steps` | **不在** Hub `trial_model` 中；从展开后的 `result.json` 抽 |

finalize 时 **只解析** job/trial 的 `config.json`、`result.json`，以及可选 `lock.json`（供 overlay.runner）。`agent/`、`verifier/`、`peri.txt` 保留在 S3，不把数百 MB 日志写入 SQLite。

对每个 trial，读取 `result.json` 的 `agent_result`（若结构不同，同时容忍顶层同名字段）中的：

- `n_input_tokens`
- `n_cache_tokens`
- `n_output_tokens`
- `cost_usd`
- `n_agent_steps`（若仅有 `n_steps` 且语义为 agent 步数，映射到此列；否则不猜）

Hub `trial_model` 可能带 token/cost（官方 insert 在值为 null 时 **省略键**，不是写 0）。分析表仍按下列规则 0 填；**不以** Hub 省略当作「测得 0」。

规则：

1. 字段为 null、缺省、空字符串、JSON `null` → 该列存 **0**。
2. 字段为合法数字 → 原样存储（可以为 0）。
3. 五个字段**全部**属于第 1 类 → `usage_reported=false`，数值全 0。
4. 至少一个字段属于第 2 类 → `usage_reported=true`；仍缺的字段存 0。
5. **禁止**用非零常数、其它 job 的均值、`tool_use_events_unverified` 或官方榜数字填充。

job 级：

1. 若 job `result.json` 已有 job 级 token/cost/steps 且为数字，用源值；源缺的键填 0。此时若任一源键为数字，则 `jobs.usage_reported=true`。
2. 若 job 级全部缺失：`jobs.n_*=SUM(trials.n_*)`，`jobs.cost_usd=SUM(trials.cost_usd)`，`jobs.usage_reported` = 任一行 trial `usage_reported` 为真。对照包即此情形，合计为 0，`usage_reported=false`。
3. 列表/详情/聚合 **必须输出这些数字**，不得因 `usage_reported=false` 删键。

对照包全部 trial 的 token/cost/steps 为 null：入库后应为 0，且 `usage_reported=false`。这是验收断言，不是「接口可以不返回用量」。

### 3.7 幂等、并发、锁

Harbor 官方语义（源码 `Uploader.upload_job`，已核实）：

- 幂等键是 **job UUID**（`result.json` 的 `id`），不是内容哈希。
- 已存在的 job：不改 trial 数据；`archive_path` 已设的 trial **跳过**；job 级 `archive_path` 已设则 **跳过 finalize**。
- 未带 `--public/--private` 时不改 visibility；显式 flags 才 PATCH visibility。
- 显式 `--org` 与已有 owner 冲突则 CLI 报错；v1 兼容面见 4.4，不实现 transfer。

因此：**禁止**再对「同一 `job_id`、不同内容」返回 409。内容不一致时保持已有行，可打日志。这与旧设计的 `ingest_sha256` 冲突语义不同。

SQLite 写者：ingest 路径（兼容表写入 + finalize 分析表）与 overlay PUT。CLI 默认 `--concurrency 10`，trial 插入并发。实现：

1. `busy_timeout=5000`。
2. 进程内 `sync.Mutex`（或按 `job_id` 分片锁）包住所有 SQLite 写。S3 Put 可在锁外；写 `archive_path` 与分析表必须在锁内。
3. v1 单进程。不假设多副本写同一 sqlite 文件。若日后多副本，需换集中锁或放弃共享文件；本期不设计。

finalize 分析写入与兼容表更新同一事务：失败则 `hub_job.archive_path` 保持 NULL，CLI 重试会再 finalize。

---

## 4. Harbor 上传协议：已核实 vs 假定

公开文档（[Upload](https://docs.harborframework.com/core-concepts/harbor-hub/upload.md)、[Hosted CLI](https://docs.harborframework.com/core-concepts/hosted-harbor/cli.md)、[Hosted API](https://docs.harborframework.com/core-concepts/hosted-harbor/api.md)，检索日 2026-09-16）**没有**给出「上传已完成 job」的 HTTP 方法/路径/请求体规格。文档只规定 CLI 行为，以及 **另一套** hosted 拉起 API。

因此本服务的 ingest **不是**自拟 REST 目录，而是对官方 CLI 客户端的 **薄兼容面**：路径与字段以 [harbor-framework/harbor](https://github.com/harbor-framework/harbor) `main` 的 `src/harbor/upload/`、`src/harbor/auth/tokens.py`、`src/harbor/storage/resumable.py` 为准。未在源码钉死的头字段、状态码细节标为 **待对照 Harbor 源码/抓包**。不实现 Hub 上其余产品接口。

### 4.1 公开文档已核实（CLI / 产品语义）

| 事实 | 来源 |
| --- | --- |
| `harbor auth login` 后 `harbor upload "<job-path>"`；目录必须有 `config.json` 与 `result.json` | docs upload |
| `harbor run … --upload` 随 trial 结束流式上传，结束时 finalize job archive；失败可再 `harbor upload` | docs upload |
| `--launch` 与 `--upload` **互斥**；`--public` / `--private` / `--share` 仅 upload 侧 | docs hosted CLI |
| 新上传默认 private；重传不带可见性 flag 则保持服务端值 | docs upload |
| `--org` 指定 owner；重传不能改 owner | docs upload + CLI `upload.py` |
| `--share`（文档现用名；源码 Option 名为 `--share`）可重复；非成员 org 需 `--yes` | docs upload |
| 已上传 trial 重传跳过 | docs upload |
| hosted 拉起：`POST {supabase}/functions/v1/job-submit`，`Authorization: Bearer sk-harbor-…`，**必须** `Idempotency-Key`；`GET /job-status` | docs hosted API / submitting-jobs |
| hosted 拉起 **不是** 完成 job 的上传 | 同上；Uploader 不调用 `job-submit` |

### 4.2 源码已核实（CLI 实际上传的线协议）

指向本服务的方式（`src/harbor/auth/constants.py`）：

```text
export HARBOR_SUPABASE_URL=http://127.0.0.1:<port>    # 仅 loopback 允许 http
export HARBOR_SUPABASE_PUBLISHABLE_KEY=<与本服务 EVAL_DISPLAY_ANON_KEY 相同>
export HARBOR_API_KEY=<与本服务 EVAL_DISPLAY_TOKEN 相同>
harbor upload jobs/peri-3142-full
```

CLI **没有** `--upload-url` 这类单独开关；上传目标就是 `HARBOR_SUPABASE_URL`。

**不是** 一次 multipart zip POST。流程（`Uploader`）：

1. `POST {SUPABASE_URL}/functions/v1/api-key-exchange`  
   - Headers：`apikey: {publishable key}`，`Content-Type: application/json`  
   - Body：`{"api_key":"<HARBOR_API_KEY>"}`  
   - 200：`{"access_token":"<JWT>","expires_in":<秒>}`。`expires_in` 必须为 **正数**（CLI 用其计算过期；0 会导致立刻重换票）。JWT 须含 `sub`（CLI `jwt.decode(verify_signature=False)` 取用户 id）。
2. 此后 PostgREST / Storage 请求带 `Authorization: Bearer <JWT>`（以及 supabase-py 惯用的 `apikey`）。
3. `start_job`：`GET job.visibility` → 无行则 `INSERT job`（`archive_path`/`finished_at`/`log_path` 为 null）；有行则按 flag 决定是否 `PATCH visibility`。默认调用 `rpc/list_my_orgs` 解析 owner。
4. 每 trial：upsert `agent`、`model`；`upsert trial`（`on_conflict=id`，`ignore_duplicates`）；上传 `trials/{id}/trial.tar.gz`；可选 trajectory；`PATCH trial` 写 `archive_path`。
5. `finalize_job`：上传 `jobs/{id}/job.tar.gz`（及可选 `job.log`）；`PATCH job` 写 `archive_path`、`finished_at`。

归档格式是 **tar.gz**（不是 zip）。trial allowlist：`config.json`、`lock.json`（**必有**，否则 CLI 本地失败、请求到不了本服务）、`result.json`、`analysis.md`、`agent`、`verifier`、`artifacts`、`trial.log`、`exception.txt`。job allowlist：`config.json`、`lock.json`、`result.json`、`analysis.md`、`job.log`，外加各 trial 子目录按 trial allowlist 打包。

Storage：

- bucket 名 `results`。
- 小对象：supabase-py `storage.from_("results").upload`。
- 大于 6 MiB：TUS `1.0.0`，endpoint `{SUPABASE_URL}/storage/v1/upload/resumable`（URL **不是** `*.supabase.co` 时与 API 同主机，见 `resumable.py`）。Metadata：`bucketName`、`objectName`、`contentType`、`cacheControl`（base64 进 `Upload-Metadata`）。PATCH 的 `Content-Type` 为 `application/offset+octet-stream`。已存在对象：HTTP 409 或 body 含 `already exists` 则视为幂等成功。

PostgREST 资源名（表名，不是 `/v1/jobs`）：`job`、`trial`、`agent`、`model`、`trial_model`。查询为 PostgREST 语法（`id=eq.<uuid>`，`select=`，`on_conflict=`），**不是** 路径参数。

### 4.3 公开 HTTP 规格缺失之处（待对照源码/抓包）

下列实现时以一次真实 `harbor upload` 对本地服务的抓包为准，设计先给可实现的最小假设；**假设不等于官方文档**。

| 项目 | 假定（可实现） | 待对照 |
| --- | --- | --- |
| PostgREST `Prefer` / `Accept` | 支持 `return=representation`、`return=minimal`、`resolution=merge-duplicates`、`resolution=ignore-duplicates`；`maybe_single` 常用 `Accept: application/vnd.pgrst.object+json`，0 行时返回 406 或空 body，CLI 视为不存在 | 精确状态码与空结果形状 |
| `Range` 分页 | `list_trials_for_job` 用 `.range(start, start+999)` | 是否必回 `Content-Range` |
| Storage 小对象路径 | `POST /storage/v1/object/results/{objectName}`，objectName 可含 `/` | 是否 `x-upsert`、是否 PUT |
| TUS `Location` | 绝对或相对 URL，主机必须等于 `HARBOR_SUPABASE_URL`（CLI 校验 origin） | 路径模板（`/storage/v1/upload/resumable/{id}` 等） |
| JWT 签名 | 本服务 HMAC（`EVAL_DISPLAY_JWT_SECRET`）自签；**不**验证官方 Hub JWT | 官方是否还校验 `iss`/`role`；CLI 不校验签名 |
| `apikey` 头 | 必须等于 `EVAL_DISPLAY_ANON_KEY`，否则 401 | supabase-py 是否每请求都带 |
| `created_by` RLS | 无 Postgres RLS；鉴权=换票成功。不模拟 Hub RLS 全文 | Hub 策略细节 |
| upsert 响应体 | `return=representation` 时返回含 `id` 的对象数组（`upsert_agent` 读 `data[0]["id"]`） | 列集合是否必须与 Hub 完全一致 |
| `org_id` 列 | insert 可带可缺；缺则按 CLI 遗留路径 | 生成类型未列入 `PublicJobInsert`，db_client 动态加键 |

### 4.4 明确不实现的 Hub 面

以下 **有** 公开 HTTP 或 CLI，但不属于「上传已完成 job」，v1 **禁止**做成展示服务的产品 API：

- `POST /functions/v1/job-submit`、`GET /job-status` 及一切 hosted secret / registry-credential。
- `POST /rest/v1/rpc/add_job_shares`、组织成员表、Hub job list/compare/delete。
- 未在 Uploader 调用链上的其它 PostgREST 表。

未实现的 `POST /rest/v1/rpc/{name}` 统一返回 PostgREST 式 `{"code":"PGRST202",...}` 与合适 404，以便 CLI 把 `list_my_orgs` 缺失当成「尚未部署 org 所有权」并走 **无 org_id** 遗留上传（`UploadDB.resolve_owner_org` 已核实）。v1 **推荐实现** `list_my_orgs` 最小桩（见 API-07），这样不依赖该降级分支；`--share` 仍不支持。

对照包若缺 trial `lock.json`，官方 CLI 在本地打包失败。这是客户端约束。验收夹具须具备 CLI 所需文件，或测试直接打 tar 走 Storage；**不得**在服务端编造 lock。

---

## 5. 接口清单

共 **18** 个对外 HTTP 路由。计数规则：Harbor 兼容面按 **资源路径** 计 9 条（同一路径上的 GET/POST/PATCH 或 TUS POST/HEAD/PATCH 算一条，细节在各条内）；展示面 9 条（探活 2 + 读 6 + overlay PUT 1）。不把 PostgREST 每个 query 组合算成独立产品 API。

鉴权分三套，不要混用职责：

- **Harbor 兼容面（API-01…API-09）：** `HARBOR_API_KEY` 换票后的 Bearer JWT；请求头 `apikey` = `EVAL_DISPLAY_ANON_KEY`。v1 将 `HARBOR_API_KEY` 配成与 `EVAL_DISPLAY_TOKEN` 同一值（仅 ingest 换票，不是前台读接口密钥）。
- **展示面（API-10…API-17 的 GET，以及前台静态页）：** 公开，无需令牌。
- **后台（API-18 overlay PUT 与 `/v1/admin/*`）：** `Authorization: Bearer <EVAL_DISPLAY_ADMIN_TOKEN>`。缺头或无效 Bearer → `401`。Harbor `EVAL_DISPLAY_TOKEN` 调用后台 → `403`。生产必须设置 `EVAL_DISPLAY_ADMIN_TOKEN`。

展示面统一错误体：

```json
{
  "error": {
    "code": "trial_invalid",
    "message": "trial missing task_checksum",
    "details": [{"trial_id": "...", "field": "task_checksum"}]
  }
}
```

兼容面错误体跟 PostgREST / Storage 形状（`code`、`message`、`details` 等），**待对照抓包**；不要把展示面错误体套到 supabase-py 上，以免 CLI 解析失败。

自定义 `POST /v1/jobs` **不是**本清单中的接口。

### 5.1 Harbor 兼容 ingest（9）

#### API-01 `POST /functions/v1/api-key-exchange`

- **目的：** CLI 换短时 JWT。无此接口则官方 CLI 不能指向本服务。
- **请求：** 见 4.2。
- **响应 `200`：** `{"access_token":"<jwt>","expires_in":900}`。JWT `sub` 用稳定内部用户 id（v1 单租户可固定 UUID）。
- **错误：** api_key 无效 → `401`（CLI 当密钥作废）。

#### API-02 `/rest/v1/job`（GET / POST / PATCH）

- **GET：** `select` + `id=eq.{uuid}`（及 CLI 用到的列清单）。0 行：按 maybe_single 语义。用于 visibility 探测、读 `archive_path` 以决定是否 finalize。
- **POST：** insert。body 字段对齐 `PublicJobInsert` + 可选 `org_id`。`id` 即 Harbor `job_id`。
- **PATCH：** `id=eq.{uuid}`。用于 visibility 与 `finalize_job`（`archive_path`、`finished_at`、`log_path`）。
- **幂等：** 同一 `id` 再次 POST：返回 Hub 式唯一约束错误（CLI 先 GET 再决定 insert，正常路径不应撞；并发下 **待对照** supabase 23505 形状）。PATCH finalize 在已有 `archive_path` 时保持原值（CLI 本不应再调；防御性）。
- **副作用：** 仅写 `hub_job`。**不**在此时写分析表 `jobs`。

#### API-03 `/rest/v1/trial`（GET / POST / PATCH）

- **GET：** 按 `id` 或 `job_id` + `order` + `Range`。resume 用 `archive_path` 是否非空。
- **POST：** upsert，`on_conflict=id`，忽略重复（官方 `ignore_duplicates=True`）。列对齐 `insert_trial`：含 `task_content_hash`、`lock`、`rewards`、`agent_id`、阶段时间戳等。
- **PATCH：** `finalize_trial_artifacts`：`archive_path`、`trajectory_path`。官方要求更新恰好一行，否则 CLI 抛错。
- **副作用：** 写 `hub_trial`。分析表仍等 job finalize。

#### API-04 `POST /rest/v1/agent`

- upsert，`on_conflict=added_by,name,version`。响应必须能让 CLI 读到 `id`。

#### API-05 `POST /rest/v1/model`

- upsert，`on_conflict=added_by,name,provider`。body **省略** `provider` 时按官方注释视为 DB 默认 `'unknown'`，不要写 JSON null（会撞 NOT NULL）。

#### API-06 `POST /rest/v1/trial_model`

- upsert，`on_conflict=trial_id,model_id`，忽略重复。token/cost 键可缺。**无** `n_agent_steps` 列。

#### API-07 `POST /rest/v1/rpc/list_my_orgs`

- **目的：** `start_job` 解析默认 personal org / `--org`。
- **v1 桩：** 返回单元素数组 `[{ "id": "<固定UUID>", "name": "local", "display_name": "local", "kind": "personal", "role": "owner" }]`。请求体 `{}`。
- 不实现真实多 org。`--org` 仅当名称匹配该桩。`--share` 不实现。

#### API-08 Storage 对象写入 `POST /storage/v1/object/results/{*objectName}`

- **目的：** 小文件（官方阈值以内；实现上 TUS 与小对象都应落到同一 S3 键）。
- **鉴权：** Bearer JWT。
- **行为：** 字节写入 `s3://{bucket}/{objectName}`。已存在 → 409 且 body 含 `already exists`（官方 `_is_already_exists`）。
- 路径模板 **待对照抓包**；若 supabase-py 实际走 `PUT` 或 query `?name=`，按抓包改，不另造第三套路径。

#### API-09 TUS `/storage/v1/upload/resumable`

- **方法：** `POST` 创建（`Upload-Length`、`Upload-Metadata`、`Tus-Resumable: 1.0.0`）→ `201` + `Location`；`HEAD` 读 `Upload-Offset`；`PATCH` 写 chunk。
- **元数据：** `bucketName=results`，`objectName` 见 3.1。
- 完成后对象与 API-08 同一 S3 键。阈值与 chunk 6 MiB 为客户端行为，服务端按 offset 协议实现即可。
- Hertz 必须按 **原始 body** 处理 PATCH，不要当 JSON 绑定。见 7.1。

**job finalize 之后（服务端内部，非新的对外 API）：** 若 `hub_job.archive_path` 刚从空变为非空：从 S3 取 `job.tar.gz`（不足则拼各 `trial.tar.gz`），展开到 `jobs/{job_id}/files/`，校验并写入分析表（3.6）。校验失败：不写分析表，`archive_path` 回滚或保持空并返回 PATCH 5xx，以便 CLI 重试。校验规则与旧 zip ingest 相同：

1. job `id` 为 UUID；所有 trial 的 `job_id` 与之相等。
2. 每个 trial 必须有 `id`、`task_checksum`、`verifier_result.rewards.reward`。
3. `reward` ∈ {0, 1}，否则失败。
4. `agent_info.name` 非空。
5. token/cost/steps 缺省**不**构成失败，按 3.6 写 0。
6. 无 trial → 失败。

`EVAL_DISPLAY_MAX_UPLOAD_BYTES`：单对象上限，建议默认 **512 MiB**（trial tar 可含 peri.txt）。超限 413。不要用旧的 64 MiB 假设（那是「跳过 agent 日志的自定义 zip」）。

### 5.2 展示读 / overlay（9）

前缀 `/v1`（探活除外）。

#### API-10 `GET /healthz`

- **目的：** 进程存活。不强制连库或 S3。
- **请求：** 无。可匿名。
- **响应 `200`：** `{"status":"ok"}`

#### API-11 `GET /readyz`

- **目的：** 就绪：能对 SQLite `SELECT 1`。
- **请求：** 无。可匿名。
- **响应 `200`：** `{"status":"ready"}`
- **错误：** `503` `{"error":{"code":"db_unavailable","message":"..."}}`  
  v1 不把 S3 探活做成 ready 条件（ingest 时失败即可）。

#### API-12 `GET /v1/jobs`

- **目的：** job 列表（一行一个 job，**不得**把 full 与 fail44 合成一行）。只含分析表已 finalize 的行。
- **查询：** `limit`（默认 50，最大 200）、`offset`、`job_name`（精确）、`agent_name`、`model_name`（后二者精确匹配分析表 `jobs` 上 finalize 写入的多数身份，不扫 `trials`）、`job_type`（`H`/`M`/空表示只查 null）、`attestation_status`。
- **响应 `200`：**

```json
{
  "total": 2,
  "items": [
    {
      "job_id": "b7757551-4f4f-45d2-9580-2e30aa8a6077",
      "job_name": "peri-3142-full",
      "agent_name": "peri",
      "agent_version": "agent-v3.14.2",
      "model_name": "deepseek-v4-flash",
      "model_provider": "deepseek",
      "n_trials": 113,
      "n_reward_1": 69,
      "pass_at_1": 0.61061947,
      "n_input_tokens": 0,
      "n_cache_tokens": 0,
      "n_output_tokens": 0,
      "cost_usd": 0,
      "n_agent_steps": 0,
      "usage_reported": false,
      "started_at": "2026-09-11T10:37:24.869499Z",
      "finished_at": "2026-09-12T23:19:13.803925Z",
      "duration_sec": 131868.934,
      "job_type": null,
      "endpoint_class": "cloud",
      "attestation_status": "unsigned",
      "incomparability": "非 Pier 官方 DeepSWE 榜。执行器是 Pier，harness 是 Peri，不是官方 Pier + mini-swe-agent 协议。",
      "runner_name": "pier",
      "runner_version": "0.3.1"
    }
  ]
}
```

`duration_sec`：两端时间均有则 `finished_at - started_at`（秒，浮点），否则 `null`。列表不返回四阶段分位数（见 API-16）。

- **错误：** 本接口公开，无需令牌。

#### API-13 `GET /v1/jobs/{job_id}`

- **目的：** 单 job 详情（含 overlay；默认不含 trial 数组）。
- **查询：** `include=config,result` 时从 S3 `files/` 附加 `harbor_config` / `harbor_result`。不要为列表去拉对象。
- **响应 `200`：** API-12 的 item 全部字段，加上 `hub_org`、`harbor_version`、`dataset_path`、`dataset_ref`、`dataset_name`、`sandbox_type`、`sandbox_location`、`job_type_reason`、`attestor`、`n_errors`、`n_retries`；可选两个 JSON。
- **错误：** `404` `job_not_found`。本接口公开，无需令牌。

#### API-14 `GET /v1/jobs/{job_id}/trials`

- **目的：** 该 job 下 trial 列表。
- **查询：** `limit`（默认 100，最大 500）、`offset`、`reward`（`0` 或 `1`）、`task_name`（子串）、`order`（`task_name` | `started_at` | `reward`，默认 `task_name`）。
- **响应 `200`：**

```json
{
  "job_id": "b7757551-4f4f-45d2-9580-2e30aa8a6077",
  "total": 113,
  "items": [
    {
      "trial_id": "<uuid>",
      "task_name": "datacurve/...",
      "task_checksum": "...",
      "agent_name": "peri",
      "model_name": "deepseek-v4-flash",
      "reward": 1,
      "f2p": null,
      "p2p": null,
      "exception_type": null,
      "started_at": "...",
      "finished_at": "...",
      "duration_sec": 956.094,
      "n_input_tokens": 0,
      "n_cache_tokens": 0,
      "n_output_tokens": 0,
      "cost_usd": 0,
      "n_agent_steps": 0,
      "usage_reported": false
    }
  ]
}
```

- **错误：** `404` job 不存在。本接口公开，无需令牌。

#### API-15 `GET /v1/jobs/{job_id}/trials/{trial_id}`

- **目的：** 单 trial 详情。
- **响应 `200`：** API-14 item 全部标量，加上 `agent_info`、`verifier_rewards`、`exception_info`、四阶段 TimingInfo、可选从 S3 读取的 `harbor_config` / `harbor_result`（本接口默认带上，因单 trial 各一份）、`trajectory_uri`（v1 恒为 `null`）。用量五字段与 `usage_reported` 必有。
- **错误：** `404` `trial_not_found`。本接口公开，无需令牌。
- **不做：** 不返回 peri.txt，不把 metadata.stream 渲染成 messages[]。

#### API-16 `GET /v1/jobs/{job_id}/aggregate`

- **目的：** 该 job 的聚合（榜单详情：Pass@1、n、agent、model、用量、可选时长）。
- **响应 `200`：**

```json
{
  "job_id": "b7757551-4f4f-45d2-9580-2e30aa8a6077",
  "job_name": "peri-3142-full",
  "agent_name": "peri",
  "agent_version": "agent-v3.14.2",
  "model_name": "deepseek-v4-flash",
  "n_trials": 113,
  "n_reward_1": 69,
  "n_reward_0": 44,
  "pass_at_1": 0.61061947,
  "pass_at_1_fraction": "69/113",
  "n_input_tokens": 0,
  "n_cache_tokens": 0,
  "n_output_tokens": 0,
  "cost_usd": 0,
  "n_agent_steps": 0,
  "usage_reported": false,
  "n_usage_reported_trials": 0,
  "duration_sec": {"n": 113, "min": 421.293, "p50": 956.094, "mean": 1169.091, "max": 3767.702},
  "phase_duration_sec": {
    "environment_setup": {"n": 113, "min": 0, "p50": 0, "mean": 0, "max": 0},
    "agent_setup": {},
    "agent_execution": {},
    "verifier": {}
  }
}
```

时长分位数在 v1 **实现**（对照包有 TimingInfo，可算）。若某阶段缺时间戳，该阶段 `n` 为有两端时间的 trial 数，不拿 0 时长冒充。用量即使全 0 也要出现在 JSON 中。上表 duration 数字若用于对照包验收，须与 overlay 中该包实测聚合一致，不是 SLA。

- **错误：** `404`。本接口公开，无需令牌。

#### API-17 `GET /v1/compare`

- **目的：** 两个 job 并排。
- **查询：** `job_a`、`job_b`（UUID，必填）。
- **逻辑：** 各返回与 API-16 相同的聚合对象。`dataset_match`：双方 `dataset_ref` 均非空且相等 → `true`；否则若双方 `dataset_path` 非空且相等 → `true`；否则 `false`（对照包 full 与 fail44 的 path 不同，应为 `false`）。`overlap_by_checksum`：两边 `task_checksum` 交集大小。不自动合并为一条榜。
- **响应 `200`：**

```json
{
  "dataset_match": false,
  "overlap_n": 44,
  "job_a": {"job_id": "...", "pass_at_1": 0.61061947, "n_input_tokens": 0, "cost_usd": 0, "n_agent_steps": 0, "usage_reported": false},
  "job_b": {"job_id": "...", "pass_at_1": 0.45454545, "n_input_tokens": 0, "cost_usd": 0, "n_agent_steps": 0, "usage_reported": false}
}
```

`job_a` / `job_b` 对象形状与 API-16 相同（可省略 `phase_duration_sec` 以减小载荷，但用量五字段必留）。

- **错误：** `400` 缺参数；`404` 任一 job 不存在。本接口公开，无需令牌。

#### API-18 `PUT /v1/jobs/{job_id}/overlay`

- **目的：** 后补我方字段（Harbor JSON 已入库、CLI 上传未带 overlay）。
- **请求体：** `evo-harness.job-overlay.v1` 的 `filled` 子集，未知键进 `extra`。不允许写 `pass_at_1`（由 M4 从 reward 计算）。不允许写非零 token 覆盖 Harbor 抽出列。
- **响应 `200`：** 当前 overlay 全量 + `job_id`。
- **错误：** `401`；Harbor `EVAL_DISPLAY_TOKEN` 或其它非后台令牌 → `403`/`401`；`404`；`422` `job_type` 非 `H`/`M`/null。

GET overlay 不单列接口，走 API-13。overlay 另有 `listed`（默认 true）：false 时前台列表/详情隐藏，后台仍可见。

### 5.3 后台管理（最小集）

静态页 `GET /admin/`（匿名 HTML）。接口均需后台令牌：

| 方法 | 路径 | 目的 |
| --- | --- | --- |
| GET | `/v1/stats` | 前台总览：作业数、trial 数、作业 Pass@1 均值（不混池）、用量已报告/未报告作业数。公开，无需令牌。 |
| GET | `/v1/admin/status` | sqlite 路径、s3 backend 种类、ready、hub/finalize 计数。不返回密钥。 |
| GET | `/v1/admin/jobs` | 含未 finalize 的 `hub_job`。 |
| GET | `/v1/admin/jobs/{id}` | 单作业（含 unlisted）。 |
| DELETE | `/v1/admin/jobs/{id}` | 删分析行、hub 行与 S3 前缀。 |

---

## 6. 聚合规则

1. **Pass@1** = 该 job 内全部已入库 trial 的 `reward` 算术平均，`reward` 只允许 {0,1}。等价于 `n_reward_1 / n_trials`。Hub 不自动打分；分数只在本服务计算。
2. **分母** 是入库 trial 数，不是数据集宣称的全量题数。smoke / fail 子集 / full 各是独立 job、独立一行。
3. **禁止混榜：** `peri-3142-full`（113 trial，该包实测聚合 69/113）与 `peri-3142-fail44-v4flash`（44 trial，该包实测聚合 20/44）不得合成一个 board 行。fail44 的 44 题是 full 中 `reward=0` 的子集，这是 overlay 叙述，不是自动 join 键。API-12 一行一 `job_id`。
4. **agent / model 展示：** 取该 job 下 trial 的 `agent_info`；若多值，聚合接口返回出现次数最多的一对，并加 `agent_model_consistent: false`（v1 对照包一致）。不使用 null 的 `config.agent.name`。
5. **时长：** trial `duration_sec = finished_at - started_at`。四阶段各自用 TimingInfo 两端时间。列表可只给 job 墙钟时长；分位数只在 API-16。缺时间戳则该样本不进入该列的 n/min/p50/mean/max。
6. **用量：** job 级数字按 3.6。接口始终给出五字段。当 `usage_reported=false`（源缺失被写成 0）时，**0 表示「这次 run 未报告用量」，不是测得的零 token / 零成本 / 零步**；不得把此类 0 拿去对比模型成本或做资源预估。
7. **步数：** 只承认 Harbor `n_agent_steps`（或明确的 `n_steps`）。对照包 overlay 中的 `tool_use_events_unverified` 不进入 `n_agent_steps`。缺省则 0。
8. **签字：** `unsigned` 作业仍出现在列表。v1 不实现「只读已签」过滤开关；查询参数可按 `attestation_status` 过滤。对外引用口径不在本服务强制，但响应必须带 `job_id`、n、agent、model、`usage_reported`、`incomparability`（若有）。

---

## 7. 目录与包结构

本仓库当前无 `go.mod`。v1 在仓库根初始化模块路径 `evo-harness`（不要虚构未存在的 `github.com/...` 路径；若日后设 remote 再改 module 名）。Hertz 入口与内部包：

```
evo-harness/
  cmd/eval-display/main.go
  internal/evaldisplay/conf/           # 环境变量：sqlite 路径、S3、token、anon key、JWT secret、addr、上传上限
  internal/evaldisplay/ingest/         # M1
  internal/evaldisplay/s3/             # M2
  internal/evaldisplay/store/          # M3：sqlc（sqlite）+ 写锁
  internal/evaldisplay/query/          # M4
  internal/evaldisplay/overlay/        # M5
  internal/evaldisplay/httpapi/        # M6：Register(h *server.Hertz)
  internal/evaldisplay/harborcompat/   # PostgREST/TUS/换票适配，由 M6 调用
  internal/evaldisplay/static/         # M7
  migrations/                          # SQLite DDL
  sqlc.yaml                            # engine: sqlite
```

`cmd/eval-display` 只负责：读配置、打开 SQLite、打开 S3 客户端、构造 Hertz `server.Default`、注册 M6 路由、优雅退出。不在 `main` 里写 SQL 或拼 S3 key。

SQLite 驱动选用 **`modernc.org/sqlite`**（纯 Go，无 CGO）：部署到无 gcc 的镜像时少一层；v1 数据量不需要 CGo `mattn/go-sqlite3` 的极限写入吞吐。sqlc 生成代码走 `database/sql`，与驱动解耦。

依赖边界：M6 → M1/M4/M5/harborcompat；M1 → M2 + M3；M4/M5 → M3；harborcompat → M2 + M3（写 hub_* 与对象），finalize 时调 M1。M2/M3 不 import Hertz。不用 ORM。不用 Redis。不用 PostgreSQL。不用 pgx。

### 7.1 为何 Hertz（相对 Gin / Fiber）

v1 负载远低于 Hertz 的设计点。选 Hertz 是与已锁定的 CloudWeGo 栈对齐，并保持 `net/http` 语义（`database/sql`、sqlc、标准中间件思路）。

上传主路径 **不是** 自定义 zip multipart，而是 JSON PostgREST + 原始字节 / TUS（`application/offset+octet-stream`）。Hertz 可注册任意方法与原始 body，**未被该协议挡住**。实现时兼容面路由应直接读 `Request.Body`，避免 JSON binder 误解析 PATCH chunk。

Gin 同样能完成这 18 个路由，但本仓库没有既有 Gin 代码。Fiber 建在 fasthttp 上，与标准 `http.Request` 不完全兼容；TUS 的 `Location` / 自定义头在标准中间件与反向代理上摩擦更大。本仓库未发现必须放弃 Hertz 的阻塞因素。若日后抓包证明必须用 HTTP/2 trailer 等 Hertz 缺口（当前源码未见），再把 **Storage 路由** 落到 `net/http`，而不是整栈换 Gin。

---

## 8. 实现顺序

按切片交付，每片可对对照包做断言。不做 chat。不把自定义 `POST /v1/jobs` 做进生产。

1. **Schema + S3 客户端：** `migrations/` 建 `hub_*`、`jobs`、`trials`、`job_overlays`；用量列 `NOT NULL DEFAULT 0`。sqlc 生成 M3。单元测试用 memory/fs；S3 兼容面（RustFS）测 Put/Get。
2. **换票 + job 行：** API-01、API-02、API-07。用 curl 模拟 CLI：换票 → insert job → GET visibility。
3. **trial + agent/model + Storage：** API-03…API-06、API-08。上传最小 `trial.tar.gz`。
4. **TUS：** API-09。用大于 6 MiB 的夹具或 CLI 真实 `--concurrency`。
5. **Finalize ingest：** `archive_path` 写入后展开 S3 `files/`、抽标量、0 填用量、`usage_reported`。对对照包断言：113 与 44 条 trial；两个 `job_id`；缺 token → 0 且 `usage_reported=false`；缺 `id`/`task_checksum`/`rewards` 则分析表不落、CLI 可重试；重复 `harbor upload` 跳过已完成 trial。
6. **列表/详情：** API-10…API-15。列表行含 Pass@1 与用量五字段。`include=config,result` 读 S3，不扫全桶。
7. **聚合与对照：** API-16、API-17。断言 full 为 69/113、fail44 为 20/44；compare 不为一条榜；`dataset_match=false`。
8. **Overlay：** API-18 后补 `incomparability` / `endpoint_class`。Harbor JSON / S3 树不被 PUT 修改。
9. **M7：** 前台 `/` 榜单与统计、后台 `/admin/` overlay/签字/删除。不要把 `eval-panel-draft.html` 的示意分数与 chat 当作 UI。
10. **CLI 联调：** 按 4.2 环境变量对真实 `harbor upload <夹具 job 目录>`（夹具须含 trial `lock.json`）。抓包回填 4.3。

验收时用该包实测聚合核对 Pass@1；用量核对「全 0 + usage_reported=false」。不要把草稿 HTML 里的 cost/token/COS 档名写入测试期望。

---

## 9. 明确不做

1. PeriAgent 开发、接入 Harbor `--agent`、Peri 官方适配器验收。
2. ATIF 轨迹、chat 面板、把 `peri.txt` 当会话。步数可以是 0；没有轨迹查看器。peri.txt 只作为 S3 作业树的一部分存在。
3. GPU / 模型 × 卡型 × 引擎资源消耗 API。Harbor 无这些字段时不编造；用量 0 且 `usage_reported=false` 不得解释为「测得零消耗」。
4. COS SKU 目录、档位 id 生成、内网 URL 展示。
5. AOS 编排层评测、控制台、把本服务嵌进 AOS。
6. 第二套 Harbor：不调度 trial、不复跑 verifier、不实现 Hub org secret、不实现 `--launch` / `job-submit`。
7. Kafka、Redis、K8s operator、公网多租户、PostgreSQL。
8. 把 Pier 官方 DeepSWE 榜与本服务行混排，或把 69/113 写成产品指标。
9. 为用量单独再加一批 HTTP 接口；用量只出现在已有 list/detail/aggregate/compare 响应里。
10. 发明 Hub REST 产品目录（leaderboard、share、download CLI、hosted secrets）。兼容面以 Uploader 实际调用的换票 + 五张表 + 一个 RPC 桩 + Storage/TUS 为上限。
