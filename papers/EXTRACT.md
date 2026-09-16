# Agent Harness 文献提取（五篇）

> 提取日期：2026-09-15  
> 目录：`/Users/mino/code/evo-harness/papers/`  
> 语言约定：中文叙述；论文标题、专有名词、公式、系统名保持英文。

## 下载与阅读说明

| 文件 | 来源 | 类型 | 大小 |
| --- | --- | --- | --- |
| `survey-agent-harness.pdf` | GitHub `Gloriaameng/Awesome-Agent-Harness` 的 v4 PDF（Preprints.org HTML 被 CDN 拒访；`/download` 不可用） | 真实 PDF | ~19 MB |
| `ahe.pdf` | GitHub `china-qijizhifeng/agentic-harness-engineering` 仓库内 PDF（arxiv.org 直连 RST） | 真实 PDF | ~1.3 MB |
| `life-harness.pdf` | Google Cloud `arxiv-dataset`：`2605.22166v1/v2`（9 页；附录以 arXiv HTML 补全） | 真实 PDF | ~2.1 MB |
| `harnessx.pdf` | GitHub `darwin-agent/HarnessX` 的 `HarnessX_Tech_Report.pdf` | 真实 PDF | ~2.0 MB |
| `darwinx.pdf` | Google Cloud `arxiv-dataset`：`2608.07545v1` | 真实 PDF | ~1.9 MB |

同目录另存 HTML 全文备份：`*.html.txt`（arxiv.org / Preprints.org 直连 PDF 失败时用于精读）。

---

## 总览：五篇如何串在一起

Survey 把 **harness 定义为运行时治理层**，主张「可靠性由 harness 决定，而非仅由模型决定」，并给出六元组 \(H=(E,T,C,S,L,V)\)。后面四篇都是 **在模型冻结（或几乎冻结）的前提下，把 harness 当成可进化对象**，但切面不同：

- **AHE**：编码 agent 的 *可观测闭环进化*（文件级组件 + 轨迹蒸馏 + 可证伪 change manifest）。
- **Life-Harness**：确定性环境里的 *生命周期接口适配*（合同 / 技能 / 动作实现 / 轨迹调控），强调跨模型可迁移。
- **HarnessX**：把 harness 做成 *可组合 foundry*（typed processors + AEGIS），并尝试 *harness–model 共进化*。
- **DarwinX**：把进化做成 *种群自然选择*（preserve-and-extend + archive merge），强调不牺牲已解决问题、跨 lineage 重组。

时间线（约）：Survey Preprints 2026-04 → AHE arXiv 2604.25850（2026-04）→ Life-Harness 2605.22166（2026-05）→ HarnessX 2606.14249（2026-06）→ DarwinX 2608.07545（2026-08）。后三篇彼此互引：Life-Harness 把 AHE/Meta-Harness 定位为 coding-agent 的自由代码搜索；HarnessX 把 AHE/Life-Harness 放进 self-evolving agents；DarwinX 把 HarnessX 标成「有门控但缺跨 lineage merge」。

---

## 1. Agent Harness for Large Language Model Agents: A Survey

### 元数据

- **Title:** Agent Harness for Large Language Model Agents: A Survey
- **Authors:** Qianyu Meng, Yanan Wang, Liyi Chen, Wei Wu, Yihang Li, Wenyuan Jiang, Qimeng Wang, Chengqiang Lu, Yan Gao, Yi Wu, Yao Hu
- **Venue / date:** Preprints.org preprint；DOI `10.20944/preprints202604.0428.v3`；v3 submitted 2026-04-09，posted 2026-04-28。GitHub 仓库另有 **v4** PDF。
- **arXiv id:** 无（非 arXiv）。Preprints manuscript `202604.0428`。
- **配套：** https://github.com/Gloriaameng/Awesome-Agent-Harness

### 一段摘要

这是第一篇把 LLM **agent harness** 当作独立基础设施对象的系统综述：覆盖 110+ 论文/报告/博客、23 个代表性系统。核心主张是：生产环境里 agent 的可靠性越来越由封装模型的 harness 决定，而不是模型本身；固定模型、只改 harness，已有数量级级别的经验证据。作者给出形式化定义 \(H=(E,T,C,S,L,V)\)，追溯软件测试 harness、RL 环境、早期 LLM agent 框架三条谱系，用六组件完备性矩阵给系统分类，并分析九个生产级交叉挑战与近/远期研究方向。

### 问题 / 动机

既有综述按能力模块（memory / tools / planning / safety / eval）切片，把执行环境当背景。结果是：harness–model coupling、评测基础设施引入的测量误差、跨组件耦合导致的系统不可靠——这些现象在能力视角下不可见。工程界已经把 2026 称作 “age of agent harnesses”，但缺少统一定义、分类和评测词汇。

### 「Harness」核心定义

> **Definition 2.1.** An agent harness is a software system \(H=(E,T,C,S,L,V)\) that implements six runtime governance functions.

| 符号 | 组件 | 职责 |
| --- | --- | --- |
| **E** | Execution loop | observe-think-act、终止条件、错误恢复 |
| **T** | Tool registry | 类型化工具目录、路由、监控、schema 校验 |
| **C** | Context manager | 进入 context window 的内容、compaction、检索、优先级 |
| **S** | State store | 跨 turn / 可选跨 session 持久化、崩溃恢复 |
| **L** | Lifecycle hooks | 鉴权、日志、策略、instrumentation 的前后拦截 |
| **V** | Evaluation interface | 标准化轨迹、中间状态、成功信号（不同于一般 logging） |

- **必要：** 至少实现 E 和 T 才叫 harness（否则是单轮 wrapper 或无效应器的推理引擎）。
- **充分：** 六组件都达到生产级（错误处理、鉴权、可观测、文档化失败模式）才是 full-stack harness。
- **E 的形式语义：** labeled transition system（LTS）；用它区分 ReAct（非 harness）、AutoGPT（monolithic harness）、LangGraph（topology-encoded harness）。
- **与相邻概念：** Framework = 开发时构造原语；Harness = 运行时治理；Platform = 组织级管理；Agent OS = 最严谨的 kernel 化实例；Eval harness 历史更窄，主要是 V。

### 架构 / 系统设计

Survey 本身不是系统，而是 **harness-as-infrastructure** 分析框架：

1. 单步执行：环境观察 → C 组装上下文 → 模型推理 → T 派发工具 → S 提交状态；L 拦截边界；V 记录轨迹。
2. 依赖图：E 依赖 T/C/S；L 依赖其余五者；V 依赖 L 与 S。
3. 任务类型决定组件需求：单轮 Q&A 几乎不需要 S/L；软件工程 / 长程助手 / 多智能体 / 具身任务把 S 与 L 变成「chatbot vs harness」分界。

**历史三条谱系：** 1990s 软件测试 harness（治理包装）+ RL 环境（接口契约）+ 早期 LLM 框架（失败模式目录）→ 2023–2024 “harness turn” → 2026 Harness Engineering。

**范式迁移：** prompt engineering → context engineering → harness engineering。

### 关键方法 / 算法

- 二维分类：主轴 **stack position**（可直接部署 runtime / 开发框架 / 能力模块），辅轴 **domain scope**。
- **Completeness matrix：** 23 系统 × 六组件（Full / Partial / Absent），加 security 与 multi-agent。
- 三类家族：full-stack harnesses、frameworks、capability modules（不是「更完整就更好」的一维光谱）。
- 九挑战：sandboxing、evaluation、protocol standardization、runtime context、knowledge/context engineering、tool governance、memory architecture、planning loop、multi-agent coordination；根因是 **跨组件耦合**（C↔L、V↔L、S↔T 等）。

### 实验 / 数据 / 指标 / 主要结果

Survey 不做新实验，而是汇编第三方证据（模型固定、只改 harness）：

- xAI Grok Code Fast 1：仅改 edit-tool 格式，SWE-bench **6.7% → 68.3%**（约 10×）。
- LangChain DeepAgents：TerminalBench **52.8% → 66.5%（+26%）**。
- Meta-Harness：TerminalBench-2 自动搜索达 **76.4%**，超过手写。
- AgencyBench：同一模型在 native ecosystem **48.4%**，换 harness 下降（harness–model coupling）。
- SkillsBench：skill 管理可贡献约 **+16.2 pp**，但 16/86 任务出现负增益。
- HAL：标准化 eval harness 消除大量「agent 失败」实为 harness bug。

**Taxonomy 要点：** Claude Code / OpenClaw+PRISM / Hermes / AIOS / OpenHands 为 full-stack；LangChain/LangGraph/LlamaIndex 为 framework；MemGPT/Voyager/Reflexion 为 module；HAL/AgentBench/OSWorld 为 eval infra。V 是最常缺失的组件（22 系统中 14 个 partial/absent）。

### 主张 vs 局限

**主张：** 模型能力必要但不充分；harness 是把能力翻译成可靠性的绑定约束；生产级系统倾向于六组件齐全。

**局限（文中自陈）：** 23 系统语料偏公开文档，漏掉企业内网 harness；闭源评分依赖文档；\(H=(E,T,C,S,L,V)\) 是概念分析而非因子分析；部分定量声称来自未审预印本；时间窗口止于 2026-03；OpenClaw 案例过重可能引入覆盖偏差。

### 与其他四篇的关系

Survey 完成于 2026-03、发表于 04，**早于或几乎同时于** AHE/Life-Harness/HarnessX/DarwinX 的完整实验论文。它把 Meta-Harness 列为「automated harness engineering」近端方向，但尚未纳入这四篇的闭环进化结果。这四篇可以读成对 Survey 议程的落地：AHE 对应 *agent-native observability + automated harness engineering*；Life-Harness 对应 *接口/ACI 与工具治理*；HarnessX 对应 *可组合基础设施 + 可选模型共训*；DarwinX 对应 *评测有效性、回归门控与种群搜索*。

### 值得保留的原句 / 定义

- “The agent execution harness — not the model — is the primary determinant of agent reliability at scale.”
- “model capability is necessary but insufficient for system reliability.”
- “A system must implement at minimum E and T to qualify as a harness.”
- L vs V：L 是运行时控制拦截；V 是可被外部 benchmark 消费的规范化轨迹接口。
- 六组件对应六种生产失败：execution runaway / tool misuse / context blowout / state loss / unmonitored side effects / unobservable behavior。

---

## 2. AHE — Agentic Harness Engineering

### 元数据

- **Title:** Agentic Harness Engineering: Observability-Driven Automatic Evolution of Coding-Agent Harnesses
- **Authors:** Jiahang Lin*, Shichun Liu*, Chengjun Pan*（复旦 / 北大，实习于上海奇绩智凤）；Lizhi Lin, Hang Yan, Zhenhua Han†（上海奇绩智凤）；Shihan Dou, Zhiheng Xi, Xuanjing Huang, Tao Gui†, Yu-Gang Jiang（复旦）
- **Venue / date:** arXiv preprint；页面显示最新 HTML 为 **v4**（约 2026-05）。
- **arXiv id:** [2604.25850](https://arxiv.org/abs/2604.25850)
- **代码：** https://github.com/china-qijizhifeng/agentic-harness-engineering

### 一段摘要

AHE 把编码 agent 的 harness 工程从手工工艺变成 **可观测驱动的自动闭环**：基座模型冻结，进化 agent 只改 harness。三个 observability 支柱把每次编辑变成可证伪合同——组件以文件暴露、海量轨迹蒸馏成可钻取证据、每条 edit 自报预测并由下一轮任务结果验证。10 轮把 Terminal-Bench 2 的 pass@1 从 **69.7% 提到 77.0%**，超过人手 Codex harness（71.9%）以及 ACE / Training-Free GRPO。冻结后的 harness 无需再进化即可迁移到 SWE-bench Verified（更高成功率、少 12% tokens）以及三个其他模型族（+5.1～+10.1 pp）。消融显示增益主要在 tools / middleware / long-term memory，而不是 system prompt。

### 问题 / 动机

Harness 已是长程编码性能的一等杠杆，且 **最优 harness 具有模型特异性**，模型迭代快于人工改 harness。已有自动优化多半只动 prompt / skill / playbook，很难联合进化异构组件：轨迹太长、信号被埋、框架耦合导致非 prompt 编辑易错。中心问题：进化 agent 如何 **稳定地联合进化全部可编辑组件**？作者认为瓶颈是 **observability，不是 agent 能力**。

### 「Harness」核心定义

Harness = 模型外部、可编辑的工程组件集合：system prompt、工具、middleware、skills、sub-agents、long-term memory 等，中介模型如何感知/作用于仓库、工具与执行环境。

AHE 把 harness \(H\) 实例化在 **NexAU** 上，七类正交组件作为工作区文件：

1. system prompt  
2. tool description  
3. tool implementation  
4. middleware  
5. skill  
6. sub-agent configuration  
7. long-term memory  

Seed \(H_0\)（NexAU0）刻意极简：仅 bash 工具，无 middleware/skills/sub-agents，避免把 seed 的先验算进后续增益。

### 架构 / 系统设计

闭环三角色（同一基座 GPT-5.4，隔离「分析器/编辑器更强」的混淆）：

1. **Code Agent**：在 Harbor + E2B sandbox 上跑任务。  
2. **Agent Debugger**：把轨迹当文件系统，分层蒸馏（per-task report + benchmark overview + 原始/清洗 traces，progressive disclosure）。  
3. **Evolve Agent**：只允许改 `workspace/`；runs / tracer / verifier / LLM config 只读；seed prompt 不可删。每条 edit 写入 `change_manifest.json`（失败证据、根因、预测修复任务、风险回归任务）。

**Algorithm 1：** Rollout(\(k\ge 2\)) → Clean →（第 2 轮起）Attribute 上一份 manifest 并 Rollback 失败 edit → Debugger 蒸馏 → Evolve → git Commit。保留 pass@1 最好的 \(H_{\text{best}}\)。

### 关键方法 / 算法

- **Component observability：** 失败模式映射到单一组件类；git 文件级回滚。  
- **Experience observability：** 百万级 token 轨迹 → 分层证据语料。  
- **Decision observability：** 自报预测 ∩ 下一轮任务 delta = 逐 edit 判决。  
- 并行 one-shot Explore Agent 在第 1 轮播种少量 skills，之后 Evolve Agent 可删改。  
- 约束：禁止改模型、温度、verifier、硬编码题解。

### 实验 / 数据 / 指标 / 主要结果

**优化集：** Terminal-Bench 2，89 题（easy 4 / med 55 / hard 30），每题 timeout 1h，\(k=2\) rollouts。  
**迁移：** SWE-bench Verified 500 题；跨模型：GPT-5.4 medium/xhigh、qwen-3.6-plus、gemini-3.1-flash-lite-preview、deepseek-v4-flash。  
**指标：** pass@1（基础设施中断计失败）；Tokens k。  
**主模型：** GPT-5.4 high；Evolve Agent 用 xhigh；10 轮约 32 小时。

**TB2 主结果（pass@1）：**

| Method | All | Easy | Med | Hard |
| --- | --- | --- | --- | --- |
| OpenCode | 47.2% | 75.0 | 52.7 | 33.3 |
| Terminus-2 | 62.9% | 75.0 | 74.5 | 40.0 |
| Codex | 71.9% | 75.0 | 80.0 | **56.7** |
| NexAU0 seed | 69.7% | 87.5 | 78.2 | 51.7 |
| ACE | 68.9% | 91.7 | 78.2 | 48.9 |
| TF-GRPO | 72.3% | **100** | 79.4 | 55.6 |
| **AHE** | **77.0%** | **100** | **88.2** | 53.3 |

Hard 上略低于 Codex；单独把 AHE 的 long-term memory 插入 seed 即可在 Hard 超过 Codex——说明是组件互相干扰，不是缺能力。

**SWE-bench Verified：** AHE 75.6% vs seed 75.2% / ACE 74.6% / TF-GRPO 74.2%；tokens 461k vs seed 526k（**-12%**）。ACE/TF-GRPO 在跨基准上甚至低于 seed 且更贵。

**跨模型（TB2，冻结 harness）：** deepseek-v4-flash +10.1 pp（51.7→61.8）；qwen-3.6-plus +6.3；gemini-flash-lite +5.1；同族 medium/xhigh 仅 +2.3（timeout–budget 耦合）。

**组件消融（单组件插入 seed）：** memory-only 75.3%；tool-only 73.0%；middleware-only 71.9%；prompt-only **67.4%（回归）**。三正增益之和 +11.1 pp > 完整 AHE 的 +7.3 pp → **非加性交互**。

**自归因：** fix-precision 33.7% / recall 51.4%（约 5× 随机）；regression-precision/recall ~11%（仅约 2× 随机）→ **能解释为何该修，看不见会破坏什么**。

### 主张 vs 局限

**主张：** 可观测性把 harness 进化从试错变成可证伪合同；增益可迁移，编码的是通用工程经验而非刷榜。

**局限：** 只在 TB2 进化、SWE-V 探测；未测其他语言/人机协同；timeout 按 GPT-5.4 high 拟合；不是完整 guardrail 栈；组件非加性导致 Medium 主导的折中吃掉部分 Hard 记忆增益。

### 与其他论文的关系

- 相对 Survey：把「automated harness engineering / agent-native observability」做成可运行闭环，并实证 V 级轨迹对进化的必要性。  
- 相对 Life-Harness：同为冻结模型；AHE 面向开放编码、文件级自由编辑；Life-Harness 明确批评这种 unconstrained harness-code search，改走结构化生命周期层。  
- 相对 HarnessX：HarnessX 的 AEGIS（Digester/Planner/Evolver/Critic + change manifest）与 AHE 同构，但加上 typed composition、seesaw 门控、可选模型 RL。  
- 相对 DarwinX：DarwinX 认为单 lineage + 本地门控仍会路径依赖；AHE 的 regression blindness 正好是 DarwinX 要用 preserve-and-extend / avg@k 确认去补的缺口。

### 值得保留的原句

- “this question is bottlenecked by observability, not by agent capability”
- “turn every edit into a falsifiable contract”
- “factual harness structure transfers across tasks and models whereas prose-level strategy does not”
- “the loop’s self-attribution is reliable for fixes but blind to regressions”

---

## 3. Life-Harness — Adapting the Interface, Not the Model

### 元数据

- **Title:** Adapting the Interface, Not the Model: Runtime Harness Adaptation for Deterministic LLM Agents
- **Authors:** Tianshi Xu†, Huifeng Wen†, Meng Li（北京大学）
- **Venue / date:** arXiv preprint，约 2026-05。
- **arXiv id:** [2605.22166](https://arxiv.org/abs/2605.22166)
- **代码：** https://github.com/Tianshi-Xu/Life-Harness

### 一段摘要

Life-Harness 主张：在确定性、规则治理的环境里，大量失败来自 **模型–环境接口错配**，而不是模型不会推理。它不改权重、不改评测环境，而是从训练轨迹把反复失败转成四层可复用干预：**Environment Contract / Procedural Skill / Action Realization / Trajectory Regulation**，评测时冻结。在 τ-bench、τ²-bench、AgentBench 的 7 个环境、18 个 backbone 上，126 个设置中 **116 个提升**，平均相对增益 **88.5%**。Harness 只从 Qwen3-4B-Instruct 轨迹进化，却能迁移到另外 17 个模型。与 prompt-only 进化相比平均再高约 120% 相对增益；与 tool-use 训练相比可互补甚至在域内超过专门训练的 xLAM。

### 问题 / 动机

Qwen3.5-4B 在 HMMT 数学 74.0%，在 ALFWorld 只有 43.1%——说明缺口常在观测结构、工具合同、不可执行动作、反馈无法触发恢复、轨迹退化。现有 harness 优化（AutoTTS、Workspace Optimization、HARBOR、Meta-Harness、AHE）多把 harness 当整体策略/代码搜索；本文要把 harness 看成 **稳定运行时接口**，按生命周期定位失败。

### 「Harness」核心定义

Runtime harness = 中介观察、工具、动作执行、反馈解释与轨迹控制的运行时层。

形式化：冻结 \(\theta\)，适应 \(H\)：

\[
H' \leftarrow \mathcal{A}_{\mathrm{harness}}(H,\mathcal{T}_{\mathrm{train}}),\quad \theta\ \text{fixed}.
\]

相对参数适应 \(\theta'\leftarrow\mathcal{A}_{\mathrm{param}}(\theta,\mathcal{T}_{\mathrm{train}})\)：harness 适应 **环境特异、模型无关**。

四层：

1. **Environment Contract Layer**（交互前）：\(C'=C\oplus\Delta_C\)，显式化工具规则、策略、坑。  
2. **Procedural Skill Layer**（任务条件化）：技能库 \(\mathcal{S}\)，BM25 TopK（实验中 **top-1**）注入初始 prompt。  
3. **Action Realization Layer**（执行前）：`RealizeAction` → `EXEC(a_t)` 或 `Block(m_t)`；校验/规范化/拦截必败动作。  
4. **Trajectory Regulation Layer**（执行后）：检测重复、停滞、无效重试、预算耗尽并触发恢复。

### 架构 / 系统设计

失败诊断优先级（防止后期症状掩盖接口失败）：动作实现 → 合同错配 → 轨迹退化 → 残余推理失败。

进化：冻结 Qwen3-4B-Instruct 在 **训练任务** 上反复跑；**Codex** 读 traces + 四层设计指南，改对应层；测试集始终隐藏。附录给出 Airline/Retail/Telecom/ALFWorld/WebShop/OS/DBBench 的具体合同、技能、校验器与监控器清单（大量环境特异规则，例如 ALFWorld 可行动作模糊匹配、WebShop 禁提前 buy now、DBBench 反引号修复与方言转换）。

### 关键方法 / 算法

Algorithm 1：先 EnvContract + SkillLayer，再逐步 LLM → RealizeAction → ExecuteOrBlock → RegulateTrajectory。

进化 prompt 硬约束：不得使用测试标签、不得改环境转移/评测标准；更新必须由确定性环境信号触发、尽量局部、歧义时不覆盖模型推理。

### 实验 / 数据 / 指标 / 主要结果

**环境：** Airline, Retail, Telecom, ALFWorld, WebShop, OS, DBBench。  
**进化样本：** 各环境约 30–100 条训练（ALFWorld/DB/OS/WebShop 各 100；Airline 30；Retail/Telecom 74），测试集 held-out。  
**模型：** Qwen / Llama / xLAM 共 18 个（instruct / reasoning / agent-specialized）。  
**评测：** temperature 0；τ 系列 DeepSeek-V4-Flash 作用户 LLM，报 Pass@1 与 Pass^3（三次全过）。

**18 模型平均（Table 1）：**

| Suite | Benchmark | w/o | w/ Life-Harness | Rel. Gain | Improved |
| --- | --- | --- | --- | --- | --- |
| AgentBench | ALFWorld | 41.1% | 75.7% | +84% | 17/18 |
| | WebShop | 31.4% | 44.0% | +40% | 18/18 |
| | OS | 34.7% | 41.2% | +19% | 18/18 |
| | DBBench | 48.4% | 64.6% | +34% | 18/18 |
| τ-bench | Airline Pass@1 | 49.7% | 62.6% | +26% | 16/18 |
| | Retail Pass@1 | 56.2% | 61.8% | +10% | 14/18 |
| τ²-bench | Telecom Pass@1 | 55.3% | 69.0% | +25% | 17/18 |

源模型 Qwen3-4B-Ins 上 ALFWorld **0.165 → 0.881** 量级跳跃。Leave-one-layer-out：各层在不同任务上关键不同（Airline/OS 极依赖 Action；ALFWorld 极依赖 Trajectory，去掉 -86.5%；Telecom 去 Trajectory -36.2%）。

**vs 训练：** Qwen2.5-32B + Life-Harness 在 τ-bench 超过 xLAM-2-32B **+7.5 pp**；给 xLAM 再加 harness 仍有 +6.8～28.9 pp；xLAM 在 OOD（τ² / AgentBench）甚至低于基座。

### 主张 vs 局限

**主张：** 许多失败可用环境侧结构修复；harness 与训练适应不同部位，可叠加。

**局限：** 只覆盖确定性、接口稳定的环境；开放域（目标/工具/成功标准每任务都变）难以定义稳定接口。部分 Llama 行（Retail）几乎无增益甚至微降，说明接口干预并非万能。

### 与其他论文的关系

- Survey 的 T/C/E 治理在确定性环境被拆成「合同–技能–校验–调控」四段生命周期。  
- 相对 AHE：明确不做自由 harness 代码搜索，换可审计、按层定点的干预；评测强调 **held-out tasks + 跨 18 模型**。  
- HarnessX 引用它为「可观测/可解释/改源码」一线，但 HarnessX 要 typed algebra + 可选 RL。  
- DarwinX 的 verification/contract skills 与 Life-Harness 的 Contract + Realization 层同族，但 DarwinX 在开放终端/浏览器上用种群选择而不是固定四层。

### 值得保留的原句

- “Adapting the Interface, Not the Model”
- “runtime interface adaptation … is environment-specific but model-agnostic”
- “many practical agent failures can be addressed by evolving the runtime interface rather than modifying model parameters”

---

## 4. HarnessX — A Composable, Adaptive, and Evolvable Agent Harness Foundry

### 元数据

- **Title:** HarnessX: A Composable, Adaptive, and Evolvable Agent Harness Foundry
- **Authors:** Darwin Agent Team。Core：Tingyang Chen*, Shuo Lu*, Kang Zhao*, Weicheng Meng, Kun Shao†, Jian Luan†；另有 Hanlin Teng 等 contributors（文内 `\xiaomievblue` 提示小米相关团队）。
- **Venue / date:** arXiv **2606.14249**（用户指定 v3）；实验注明 2026-05。
- **arXiv id:** [2606.14249](https://arxiv.org/abs/2606.14249)
- **主页：** https://darwin-agent.github.io/HarnessX/

### 一段摘要

HarnessX 把 harness 提升为 **一等、可序列化/可替换的配置对象** \(\mathcal{H}=(\mathcal{M},\mathcal{C})\)，行为由挂在 8 个 lifecycle hooks 上的 typed processors 经 substitution algebra 组合。其上 **AEGIS** 把 harness 进化映射为符号空间 MDP（operational mirror）：Digester → Planner → Evolver → Critic + 确定性 seesaw 门控，防御 reward hacking / catastrophic forgetting / under-exploration。可选 **harness–model co-evolution**：同一 replay buffer 同时做 AEGIS 与 cross-harness GRPO。五基准、三任务模型族、最多 15 轮，15 个配置中 14 个提升，平均 **+14.5%**（最大 +44.0% ALFWorld/Qwen3.5-9B）；共进化再 **+4.7%**。异构任务上 Global 单 harness 会崩，**variant isolation** 恢复稳定（GAIA GPT-5.4 +13.6%，peak=final）。

### 问题 / 动机

今日 harness 手写且静态、组件纠缠、轨迹不被用于系统改进、harness 工程与模型训练脱节。库/编排器/产品 harness 都没有把 harness 暴露成可替换实体，也没有 in-loop 改进。

### 「Harness」核心定义

\[
\mathcal{H}=(\mathcal{M},\mathcal{C}),\quad \mathcal{C}=(\mathbf{P},\mathbf{S})
\]

- \(\mathcal{M}\)：各角色模型与 fallback（与行为解耦）。  
- \(\mathbf{P}: \mathit{Hook}\to\mathrm{List}[\mathit{Processor}]\)：8 hooks（task_start … task_end）。  
- \(\mathbf{S}\)：单例槽（tool registry, tracer, workspace, sandbox, plugins）。

Processor 协议：`process(event) -> AsyncIterator[Event]`，五种结局：pass-through / transform / split / intercept / interrupt。

**九维 taxonomy：** D1 model selection … D9 training bridge。进化时 D2（context）与 D4（tools）最常被改。

**Operational mirror：** 状态 = \((\mathcal{H}_t,\mathcal{T}_t)\)；动作 = typed harness edit；反馈 = traces + verifier；更新 = 确定性 acceptance gate。Seesaw：不得回归任何已解决任务。

### 架构 / 系统设计

**AEGIS 四段（同一 meta-agent，选择性调用；Critic+gate 强制）：**

- Digester：~10M token 轨迹压成 per-task 证据。  
- Planner：构造 adaptation landscape，对抗 under-exploration。  
- Evolver：typed builder 操作 + change manifest + 新 processor smoke test。  
- Critic + DeterministicGate：反 reward hacking；seesaw 反遗忘。

**Variant isolation / Ensemble routing：** 最多 \(K\) 个变体，按 cluster 路由；会回归时 fork 新变体而非直接拒绝。

**Co-evolution：** Rollout → 固定 verifier 打分 → 共享 buffer → AEGIS 改 \(\mathcal{H}\) → 缓存 logprob → cross-harness GRPO 改 \(\mathcal{M}\)。模型更新 **不再额外 rollout**。

### 关键方法 / 算法

- 符号 MDP + 三病理检查清单。  
- pass@2 作为门控的二值信号（会掩盖亚阈值成功率漂移——这是后续失败分析的关键）。  
- 早期停止：连续 \(P=3\) 轮无 shipped edit。

### 实验 / 数据 / 指标 / 主要结果

**任务 agent：** Claude Sonnet 4.6, GPT-5.4, Qwen3.5-9B。  
**Meta-agent：** Claude Opus 4.6。  
**基准：** GAIA 103 / ALFWorld 134 / WebShop 100 / τ³-Bench 3 domains / SWE-bench Verified **55 题子样本**。  
**指标：** pass@2。**注意：进化集 = 评测集，无 held-out。**

**主表（peak pass@2，节选）：**

| Benchmark | Agent | Initial | Evolved | Δ |
| --- | --- | --- | --- | --- |
| ALFWorld | Qwen3.5-9B | 53.0 | 97.0 | **+44.0** |
| ALFWorld | GPT-5.4 | 76.9 | 97.8 | +20.9 |
| WebShop | GPT-5.4 | 55.0 | 73.0 | +18.0 |
| GAIA | Qwen3.5-9B | 20.3 | 37.4 | +17.1 |
| GAIA | GPT-5.4 | 73.8 | 73.8 | **0.0** |
| SWE-V (n=55) | GPT-5.4 | 45.5 | 63.6 | +18.2 |
| τ³-Bench avg | GPT-5.4 | 76.2 | 90.7 | +14.5 |

**Inverse scaling：** 越弱的任务模型增益越大。  
**Global vs isolation（GAIA GPT-5.4）：** Global R4 peak 73.8% 后塌到 49.5%（-24.3%）；isolation final=peak **87.4%**，token 更少（107.8M vs 143.7M）。  
**AEGIS vs 单 agent CC SDK：** 准确率几乎一样（87.4 vs 86.4），AEGIS 约少 12% tokens、更好审计。  
**Co-evolution：** GAIA 37.4→41.7（+4.3），WebShop 49.0→54.0（+5.0）。  
**病理实证：** GAIA reward hacking 被检出；ALFWorld under-exploration；τ³ Telecom 连续同型 edit 累积后 R7 **-14.0%**，R9 自修复。SWE-V GPT-5.4 从 peak 63.6 降到 R5 的 50.9。

### 主张 vs 局限

**主张：** 可组合接口是稳定进化的前提；进化反馈的丰富度决定能安全做多深的进化；弱模型更吃 harness。

**局限（硬）：** **没有 held-out**；离散文本动作空间；闭源 meta-agent；共进化需要同时控制 harness 与训练；SWE-V 仅 55 题；operational mirror 是设计启发式而非收敛理论。

### 与其他论文的关系

- 相对 Survey：把 \(H=(E,T,C,S,L,V)\) 细化成 hook+processor，并补上 D9 training bridge。  
- 相对 AHE：同构的蒸馏–计划–编辑–清单，但类型系统支撑 variant isolation；AHE 有跨基准/跨模型冻结迁移，HarnessX 主实验没有。  
- 相对 Life-Harness：Life-Harness 四层是固定接口模式；HarnessX 搜索更自由的 processor 代码，因此更需要门控，也更容易 reward hack。  
- 相对 DarwinX：DarwinX Table 1 把 HarnessX 标为「有 tools/control flow 与 bounded regression，但 population archive / cross-lineage merge / noise-aware avg@k 不足」。DarwinX 直接回应 Global 策略的遗忘与路径依赖。

### 值得保留的原句

- “the harness is a first-class value, the processor is a typed atomic component”
- “Language-model subagents explore, hypothesize, and propose; typed structure and deterministic gates determine what ships.”
- “the richness of the feedback signal bounds the sophistication of evolution that can be safely performed”
- Seesaw constraint：候选不得回归任何已记录的已解决任务。

---

## 5. DarwinX — Evolving Agent Harnesses Through Natural Selection

### 元数据

- **Title:** DarwinX: Evolving Agent Harnesses Through Natural Selection
- **Authors:** Yifan Zhang°, Yutong Dai°；Juntao Tan*, Luyu Yang*；Rishi Mullur, Thai Hoang, Zhiyuan Hu（Salesforce AI Research）；James Zhu†, Phil Mui†（Salesforce Agentforce）；Silvio Savarese†, Ran Xu†, Zeyuan Chen†
- **Venue / date:** 文内日期 **August 11, 2026**
- **arXiv id:** [2608.07545](https://arxiv.org/abs/2608.07545)（用户指定 v1）
- **说明：** Monet 是 Salesforce 专有 agent；DarwinX 是进化其 harness 的程序。对比一律冻结基座。
- **项目页：** https://huggingface.co/spaces/CoderDoge/darwinx

### 一段摘要

DarwinX 把 self-evolution 做成 **对 harness 变体的自然选择**：无金标、无人工钦点，只用各 benchmark 自己的 verifier 上的 **avg@k**。三个机制：(1) **preserve-and-extend**——子代必须有净增益且回归有界；(2) **archive 种群**——保留失败 lineage，按互补 solved-set merge；(3) **模块化学习信号**——失败诊断 / teacher 演示 / 自身 pass-fail contrast，一律变成 harness 编辑而非改权重。四档评测（信号与测试逐渐分离）：TB2.1 域内、TerminalWorld held-out、WebArena-Infinity 合成→真实、TB2.1→SWE-V 零样本迁移。GPT-5.5 上 Monet 75.5%→83.2%；GPT-5.6 Sol medium 达 **84.7%** 验证榜前沿；WAI audit-clean **43.5%→93.0%** 且无效轨迹 293→17。作者把增益归因于 **verification / artifact-contract** 技能族。

### 问题 / 动机

几乎所有近期工作共享内环：batch rollout → reflect → 有界 edit → 回归门控。未解决的是 **选择过程**：单 lineage 路径依赖（Robeyns et al.）；跨任务干扰（修 A 毁 B）。DGM 有 archive 但父子一对一比较，互补专家从不重组，且赢面不必保住旧能力。HarnessX 用隔离减轻干扰，却把专家留在分开的 lineage。

### 「Harness」核心定义

Harness = prompts、tools、memory、control flow（以及可改的源码层）。两层可编辑：**skill**（prompt/memory/知识）与 **code**（tools/control/agent loop）。

Fitness：每任务 \( \hat p_t(v) \)（avg@k）。子相对父：净增益 \(g=\sum\Delta_t\)，回归 \(R=\sum(-\Delta_t)_+\)。Enabler：\(g>0\) 且 \(R\le\delta\)。再经 verifier agent 两阶段（promote / probe），高保真 avg@k + preservation probe 后才能 steer 搜索。

「自然选择」按字面：没有设计师指定的更新规则，只有测量适应度下的存活。

### 架构 / 系统设计

1. **Branch evolution：** 父节点按累积 lineage gain \(G\) 采样（\(1-\beta\) 开发、\(\beta\) 拓宽）；加法编辑；探索用宽松 enabler，确认用严格 avg@k。  
2. **Population / merge：** 按 solved-set 分类 improver / specialist / stepping-stone；merge 当且仅当 \(S(\mathrm{child})\supseteq \bigcup S(v_i)\)。  
3. **Signals：** \(\nabla\) 失败轨迹；\(\pi^*\) teacher（walls：从未成功）；\(A\) 自身 k-sample 对比（variance-band）。  
4. **Shared memory \(K_g\)：** 失败主题聚合，促使造全局能力（如 setup 成本）而非逐题补丁。

TB2.1 并行分支对准能力簇：numerical ML、low-level systems、bio/assembly、parsing/text、database。

### 关键方法 / 算法

- 探索/确认分离（两速选择）。  
- 超时算真实失败；基础设施失败按协议剥离。  
- WAI：合成 300 intents + LLM judge 进化；真实 1260 题确定性 verifier；另加 JS 去混淆 + Opus 4.8 的 validity audit（evaluation-plane / privileged host / exploit 等）。

### 实验 / 数据 / 指标 / 主要结果

**RQ1 TB2.1（89 题，avg@5，GPT-5.5 high）：** Monet base **75.5%** → DarwinX **83.2%**（+7.7）；Terminus-2 同模型 78.0%。GPT-5.6 Sol medium **84.7±1.2**，对标 Claude Code+Fable5 83.8（xhigh）。簇增益集中在 ML/科学计算 +14.8、data/DB +13.8；36 改善 / 43 不变 / 9 回归。额外 compute 只砸在新解开的 6 题（turns 11→22，tokens 89k→380k）。

**RQ2 TerminalWorld：** 94 训 / 41 held-out。训练子集 0.505→1.000（proxy 过拟合），held-out Opus 4.8 **61.0%→68.3%**（25→28/41）。四个 specialist 解 24–27 题，merge 到 28——**种群吸收了 proxy 过拟合**。GPT-5.5 上 48.8→56.1，仍低于 Terminus-2 的 61.0。

**RQ3 WAI：** audit-clean **43.5%→93.0%**（+49.5）；同模型 Browser Use 86.1%。Invalid successes 120→17；confirmed invalid 23.5%→1.4%。处方/Gmail 等状态变更应用涨幅最大（+70～75 pp）。合成集上 merge 全被 revert，增益走短主 lineage。

**RQ4 SWE-V：** TB2.1 harness 原样跑 500 题，**84.2%** vs fix-skill 参考 80.8%（+3.4）。**不做 SWE-V 域内进化**（in-loop 信号是 trajectory completion 而非官方测试）。

**RQ5：** 进化 lineage 相对 base 增加 7 个 skill，全部属于 verification / artifact-contract 族（verifier-contract、graded-artifact-final-check、real-tool-artifact、security-contract-repair 等）。

### 主张 vs 局限

**主张：** 冻结模型不是冻结 agent；选择把评测算力变成可迁移能力；能力与合规可同时变好（WAI）。

**局限：** 未独立随机化 archive / parent selector / merge；机制是探索性归因；TerminalWorld n=41，McNemar 不显著；SWE-V 仅单向迁移且分数带很窄（80.8–84.2）；WAI 审计不是形式化 sandbox；公开榜不同模型/effort，真正负载的是 matched-model delta。

### 与其他论文的关系

- Survey 的 V 与「评测即选择信号」在此被做成 avg@k + preservation probe。  
- 相对 AHE：同是编码/终端；DarwinX 加种群、merge、噪声感知确认，直接打 AHE 的 regression blindness。AHE 从 bash-only seed 涨到 77.0（GPT-5.4/TB2）；DarwinX 从更强 Monet 75.5 涨到 83.2（GPT-5.5/TB2.1）——起点与协议不同，不可直接比绝对值。  
- 相对 Life-Harness：都强调合同与执行前校验；Life-Harness 层固定、环境确定性、跨模型迁移极强；DarwinX 搜索空间更大、评测阶梯更完整（held-out / 合成到真实 / 跨基准）。  
- 相对 HarnessX：共享内环与「编辑 harness 而非整 agent 代码」；DarwinX 批评 HarnessX 缺 cross-lineage merge 与严格 avg@k，并用 TB held-out 证明「proxy 满分的个体不是最好的通才」。HarnessX 有共进化，DarwinX 刻意冻结模型以归因。

### 值得保留的原句

- “the natural selection of our title is meant literally, not as a metaphor: no gold labels and no hand-picked winners”
- “a frozen model is not a fixed agent”
- “Selection is driven purely by measured fitness”
- “verification-before-finalization and contract-aware tool use”

---

## 五篇对照（精炼）

| 维度 | Survey | AHE | Life-Harness | HarnessX | DarwinX |
| --- | --- | --- | --- | --- | --- |
| 角色 | 定义 + 分类 + 挑战 | 编码 harness 自动进化 | 确定性接口适配 | 可组合 foundry + 可选共训 | 种群选择进化 |
| Harness 是什么 | \(H=(E,T,C,S,L,V)\) 治理六元组 | NexAU 七类文件组件 | 四段生命周期接口 | \(\mathcal{H}=(\mathcal{M},\mathcal{C})\) processors@hooks | Monet 的 skill+code 两层 |
| 模型 | 不论 | 冻结 | 冻结 | 主实验冻结；可 GRPO | 冻结 |
| 进化算子 | 无（议程） | 单 lineage + manifest 回滚 | Codex 改四层，然后冻结 | AEGIS 四段 + seesaw；可 isolation | preserve-and-extend + archive merge |
| 信号 | 文献 | Debugger 分层轨迹 | 训练失败 taxonomy | 全量 traces + pass@2 | 失败 / teacher / self-contrast + avg@k |
| 主战场 | 全领域综述 | Terminal-Bench 2, SWE-V | τ / AgentBench 7 环境 | GAIA, ALFWorld, WebShop, τ³, SWE-V 子集 | TB2.1, TerminalWorld, WAI, SWE-V |
| Held-out | n/a | 跨基准/跨模型，是 | 测试任务隐藏，是 | **否**（自陈） | 是（阶梯式） |
| 标志数字 | 10× / +26% 等二手证据 | 69.7→77.0 pass@1 | 116/126，相对 +88.5% | 平均 +14.5%，最大 +44% | 75.5→83.2；WAI 43.5→93.0 |
| 关键风险 | 文档偏差、定义未实证 | 回归盲目、组件非加性 | 开放域无效、规则过拟合环境 | 无 held-out、pass@2 掩盖漂移 | 算子未消融、小样本 held-out |

### 共享概念

1. **冻结模型、动 harness** 是四篇系统论文的主叙事（HarnessX 额外打开权重轴）。  
2. **轨迹必须结构化**，标量 reward 不够（Survey 的 V；AHE Debugger；HarnessX Digester；DarwinX themes）。  
3. **回归 / 遗忘** 是进化主敌（AHE rollback、Life-Harness regression check、HarnessX seesaw、DarwinX preserve-and-extend）。  
4. **合同 / 校验 / 终检** 反复出现：Survey 的 ACI、Life-Harness 的 Realization、DarwinX 的 verifier-contract、AHE 的 middleware finish-hook。  
5. **弱模型更吃 harness**（HarnessX inverse scaling；AHE 跨族弱模型 +10.1 pp；Life-Harness 小模型可逼近大模型）。

### 关键分歧

- **搜索空间：** 结构化四层（Life-Harness）≪ 文件级组件（AHE）≪ typed processor 代码（HarnessX）≈ 异构 harness 变体种群（DarwinX）。  
- **选择单元：** 下一轮任务 delta（AHE）vs 生命周期层覆盖（Life-Harness）vs 单 harness seesaw（HarnessX Global）vs 种群 archive + merge（DarwinX）。  
- **是否训模型：** 仅 HarnessX 系统做 cross-harness GRPO。  
- **科学严谨阶梯：** Life-Harness / DarwinX 更强调 held-out 与跨分布；HarnessX 主数字在进化集上；AHE 在 TB2 上进化但用 SWE-V/跨模型做转移。  
- **领域：** AHE/DarwinX 偏软件工程与终端；Life-Harness 偏确定性工具/具身/DB；HarnessX 最杂（含 GAIA）；Survey 覆盖生产 full-stack 与评测基建。

---

## 对 evo-harness 项目的直接启示（摘录级）

若后续实现进化 harness（本文件只提取、不实现）：

1. 先把可编辑面做成 **显式、可回滚工件**（AHE 文件；HarnessX processors；Survey 的六组件接口）。  
2. 进化环至少要有 **证据层 + 预测清单 + 回归门**；不要只靠「模型觉得这个 prompt 更好」。  
3. 用 **preserve-and-extend / seesaw**，并准备 **种群**，因为单 lineage 会在异构任务上遗忘。  
4. 评测必须把 **进化信号与测试** 分开（DarwinX 四档；Life-Harness 隐藏测试集）；HarnessX 自己警告了 in-domain peak 的选择偏差。  
5. 优先投资 **verification-before-finalization** 与 **动作/工具合同**，多篇独立工作都把最大增益指到这里，而不是更长的 system prompt。  
6. Prompt-only 基线（ACE、GEPA、OPRO）在这些论文里系统性弱于「改 tools/middleware/runtime」。
