# 评测系统需求：为何要做，能带来什么

## 1. 要解决的问题

AOS 默认 harness 是 Peri，也可以换成 Claude Code / Codex。COS 提供多档私有模型端点。这两件事目前都没有**同一套、可复现的任务结果**可以对照。

具体缺的是下面三类数据，不是缺一个展示页。

1. **Harness 对照（H 型）**  
   要回答「同一模型端点上，Peri 和 Claude Code / Codex 差多少」，必须同时固定：DeepSWE 数据集与 ref、模型端点、sandbox、Harbor 版本；只变 `--agent`。今天没有这样的 job。Harbor 本身支持换 agent，但结果如果只在本机目录或聊天里，无法被后续 run 引用，也无法和另一次 run 对齐字段。

2. **模型 / 部署档对照（M 型）**  
   要回答「同一 harness 下，COS 不同档端点差多少」，必须固定 harness，只变端点。Peri 的 `haiku / sonnet / opus / fable` 是 profile 别名，映射到配置里的 model id，**不是** COS 的部署档。没有 M 型 job，档位之间没有可并排的 `reward` / `cost_usd` / token。

3. **现有仓库里的评测对不上这两类问题**  
   - `peri-studio/benchmark`：mock LLM，采 peri 进程 CPU/RSS，不跑任务 verifier。  
   - `peri-fuse` eval：对已有 trace 做 LLM-as-judge，不是 DeepSWE 的程序化判分。  
   这两条都不能作为 H/M 对照的数据源。

DeepSWE 官网榜（Pier + `mini-swe-agent` + Modal）是另一套执行协议。它不包含 Peri，也不记录 COS 端点身份。不能把它的 Pass@1 当作 Peri 或 COS 的结果。第一期用 Harbor 跑 `datacurve/deep-swe-1-1`，自己聚合；默认与 Pier 官方榜不可比。

---

## 2. 为何不能只跑几次 Harbor

缺的是**记录契约和权限边界**，不是缺 CLI。

- Trial 必须能指回 `job_id` / `id`（Harbor `TrialResult`）。没有 id 的聚合数无法复跑、无法对 diff。
- Job 必须落在公司 Hub org。密钥走 org secret，不进 git、不进面板。个人 org 上的作业不能当正式记录。
- 每次 run 要标明 H 还是 M，否则 harness 差和模型差会写进同一张表。
- 必填字段要事先定死（Harbor 侧：`task_checksum`、`agent_info`、`verifier_result.rewards`、`agent_result` 的 token/cost、四阶段 `TimingInfo`、`exception_info`、ATIF `trajectory.json`；我方侧：org、dataset ref、Harbor 版本、端点档位 id、n/子集、签字）。缺字段的 trial 不能进对照。

因此需要一条固定流程：请求（锁轴）→ `harbor run --org …` → 复盘失败类型 → 签字 → 面板只读已签字 job。Harbor 负责执行和存 trial；我方负责 adapter、config 仓、端点目录、签字和面板。面板是读模型，不是第二套 runner。

---

## 3. 做成之后解决什么（按使用方）

### 3.1 AOS

可以查 H 型 job：同一 `model` 与 dataset ref 下，不同 `agent` 的 `rewards` 均值、`cost_usd`、token。每行带 trial id。

第一期 Harbor 沙箱里跑的是 agent CLI，不是 AOS 的编排服务（路由、租户、控制台切引擎）。因此结果只能说明「AOS 打算接的那类 harness」，不能说明 AOS 进程本身。若要测编排层，需要单独把 AOS 做成 Harbor agent，不在本期默认范围。

Peri 目前不在 Harbor 预集成名单里。没有 adapter 就没有 Peri 行。接入完成前，H 型可以先跑 `claude-code` 与 `codex`。

### 3.2 COS（推理引擎 / 算子优化）

可以查 M 型 job：同一 `agent` 下，不同 COS 端点的 `rewards` / `cost_usd` / token。端点在目录里用档位 id + 引擎版本标识，面板不写内网 URL。

算子优化组和「换模型」必须分开：只有声明同一权重、只改引擎或算子的两次 run，才能把差值算在优化上。否则差值只能标成端点差。

给客户看的是对照表。在少于两档正式（非 smoke 子集）对照之前，不输出「应部署哪一档」。

### 3.3 对外引用

允许引用的数字必须带：dataset ref、agent、model/端点档位、n、Harbor 版本、job/trial id，并注明非 Pier 官方口径。  
不允许：无 id 的口算、把官网榜上的云端模型分套到 COS、把 smoke 子集当全量。

面板把 ATIF 轨迹做成会话、把该 job 的 task 做成表，是为了查单题失败，不必把 Hub 账号和 secret 分给只读方。

### 3.4 评测执行 / Peri

失败先按 exception 与阶段拆：agent / 环境 / verifier / 模型，避免所有失败都记成「DeepSWE」。  
Peri adapter 的验收是：指定版本能作为 `--agent` 复现，config 进仓库。  
Harbor 已覆盖任务容器、DeepSWE verifier、agent/model 两轴、ATIF、job 存储。本期不自研这些层。代价是分数与 Pier 官方榜不可混排。
