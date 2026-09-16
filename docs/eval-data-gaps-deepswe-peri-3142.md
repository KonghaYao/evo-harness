# deepswe-peri-3142：已有数据与缺口

对照物：Harbor `TrialResult` / 我方 run 记录契约。不改原 `result.json`。可补字段写在 `fixtures/deepswe-peri-3142/our-overlay.json`。

## 原包已有（157/157 trial）

- job / trial 的 `id`、`job_id`、`task_name`、`task_checksum`、`verifier_result.rewards`（含 `reward` 0/1）
- `agent_info`：peri / agent-v3.14.2 / deepseek-v4-flash
- 四阶段 `TimingInfo`（environment_setup / agent_setup / agent_execution / verifier）
- `exception_info`：全部为 null（失败体现在 `reward=0`，不是异常中断）
- 环境：local docker
- 轨迹替代：`agent/peri.txt`（Peri stream-json，不是 ATIF）

由 `reward` 导出、写入 overlay 的聚合：

| job | n | reward=1 | Pass@1 |
|---|---|---|---|
| peri-3142-full | 113 | 69 | 69/113 |
| peri-3142-fail44-v4flash | 44 | 20 | 20/44 |

fail44 的 44 题即 full 中 `reward=0` 的 44 题。

## 我方已补（overlay）

- 执行器：Pier 0.3.1（来自 `lock.json`，不是 `harbor run`）
- `config.agent.name` 全为 null → 用 `agent_info.name=peri`
- `endpoint_class=cloud`（不是 COS）
- `job_type`：不标 H/M，记为单点基线
- `incomparability`：非 Pier 官方 DeepSWE 协议
- `attestation.status=unsigned`
- 轨迹格式标记：peri stream-json
- Pass@1 与阶段耗时统计（从已有字段计算，未另造数）

## 仍缺（不能编造）

| 字段 | 用途 | 本包情况 |
|---|---|---|
| `hub_org` | 正式记录归属 | 无 |
| `harbor_version` | 复现 | 只有 pier 版本 |
| `dataset.name` / `version` / `ref` | 钉题集 | 仅本地 path；`task_name` 有 `datacurve/` 前缀，不能当 Hub ref |
| `job_type` H 或 M | 对照轴 | 单 harness × 单模型 |
| COS 档位 id | 问题三、选型 | 云端 API，不是 COS |
| `n_input_tokens` / `n_output_tokens` / `cost_usd` | 成本与资源 | 全 null |
| `n_agent_steps` | 步数 | 全 null |
| `agent/trajectory.json` ATIF-v1.7 | chat 面板 | 无；仅 peri.txt |
| `verifier_environment_mode` | shared / separate | 全 null |
| GPU / 推理引擎名称、版本、消耗 | 问题二资源预估 | `override_gpus` 为 null，无引擎字段 |
| AOS 编排路径 | 问题一 / 问题三对 AOS | Peri CLI，未经过 AOS |
| 签字 / 请求单 id | 对外引用 | 无 |
