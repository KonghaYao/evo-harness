# Evo Agent System 的数学表达验证（v2）

> 公式：[state-model.html](./state-model.html)。给客户看的总图：[client-map.html](./client-map.html)。状态从 1 到 2（四个模块）：[entity-transform.html](./entity-transform.html)。v1：[state-model-v1.html](./state-model-v1.html)。

主题是 **evo agent system**：冻结模型，进化的是 agent harness。

---

Survey **Definition 2.1**：

\[
H=(E,T,C,S,L,V)\in\mathcal{H}
\]

| 符号 | 名字 | 管什么 | 不管什么 |
| --- | --- | --- | --- |
| \(E\) | Agent Loop | 看环境 → 想 → 动手的循环，停与纠错（LTS） | 工具清单；也不负责改 harness |
| \(T\) | Tool registry | 可调用的效应器、路由、schema | 循环怎么转；窗口里塞什么 |
| \(C\) | Context manager | 这一拍进模型窗口的内容（压缩 / 检索） | 跨会话持久化（那是 \(S\)） |
| \(S\) | State store | harness **内部**抽屉：跨 turn / 崩溃恢复 | git 里那份整体 \(H\) |
| \(L\) | Lifecycle hooks | 调用前后拦截：鉴权、日志、策略 | 对外可评测的轨迹口（那是 \(V\)） |
| \(V\) | Evaluation interface | 轨迹 / 成功信号的出口 | 打分、合入 git；外层 \(\Omega\) 读它吐出的流 |

git 里每个 commit 带一份 \(H\)。历史是 **DAG**，不是一条直线。模型在 \(H\) 之外。

---

## Base · \(H\) 的定义

Survey **Definition 2.1**：

\[
H=(E,T,C,S,L,V)\in\mathcal{H}
\]

必要：\(\{E,T\}\subseteq H\)。充分：六件齐备。

\(E\) 的语义是 LTS：

\[
E=(Q,\Sigma,\delta),\qquad \delta:Q\times\Sigma\to Q
\]

git 载荷是整份 \(H\)，不是内部的 \(S\)：

\[
\mathrm{commit}\ \Leftrightarrow\ H,\qquad S\neq\mathcal{H},\qquad M\notin H
\]

\(\Gamma\) 改 \(H\) 不改模型。

---

## 第一层 · 转换算子（验证）

\(R\) 不是单独 \(H\) 上的全函数：

\[
R:\mathcal{H}\times\mathcal{M}\times\mathsf{Env}\rightharpoonup\mathcal{T}
\]

\(\Omega\) 只消费 \(V\) 的像：\(\tau=V_H(\pi),\ o=\Omega(\tau)\)。

\(\Phi\) 保耦合不变量：\(\mathrm{WF}(H)\Rightarrow\mathrm{WF}(\Phi(H,o))\)。

合法复合用 pairing，禁止 \(\Phi\circ\Omega\circ R\)：

\[
\Gamma\;\triangleq\;\Phi\circ\langle\mathrm{id}_{\mathcal{H}},\,\Omega\circ R\rangle,\qquad
\Gamma(H)=\Phi\bigl(H,\Omega(R(H))\bigr)
\]

\(\Gamma:\mathcal{H}\to\mathcal{H}\) **不定** \(\mathrm{Acc}\)。提案不是判决。

---

## 评判 · \(J\)（进化标准）

观测拆成搜索 / 持有，封口：

\[
o=(o_{\mathrm{s}},o_{\mathrm{h}})\in\mathcal{O}_{\mathrm{s}}\times\mathcal{O}_{\mathrm{h}}
\]

\[
\nu:\mathcal{O}_{\mathrm{s}}\to\mathbb{R},\qquad
\mathrm{Imp}(H,H^{+})\iff \nu(o_{\mathrm{s}}^{+})\ge\nu(o_{\mathrm{s}})
\]

\[
\mathrm{Pres}(H,H^{+})\iff \mathrm{Sol}(H)\subseteq\mathrm{Sol}(H^{+})
\]

一次成功进化：

\[
\mathrm{Acc}(H,H^{+})\iff \mathrm{WF}(H^{+})\land\mathrm{Pres}(H,H^{+})\land\mathrm{Imp}(H,H^{+})
\]

期末考试不进门控：\(\mathrm{Exam}(H)=\nu_{\mathrm{h}}(o_{\mathrm{h}})\)，\(\mathrm{Exam}\notin\mathrm{Acc}\)。

第二层：\(\mathsf{Fwd}(A,B)\Rightarrow H(B)=\Gamma(H(A))\land\mathrm{Acc}(H(A),H(B))\)。

---

## 第二层 · git DAG

仓库 \(\mathcal{R}\) 是带 ref 的 commit DAG。每个 commit \(c\) 有载荷 \(H(c)\) 和双亲 \(\pi(c)\)。

**正向**（以前的 A→B 只是这个）：

\[
\mathrm{fwd}(A)=B,\qquad H_B=\Gamma(H_A),\qquad \pi(B)=\{A\}
\]

HEAD 从 \(A\) 挪到 \(B\)，不删除 \(A\)。反复 \(\mathrm{fwd}\) 才是线性的 \(H_{n+1}=\Gamma(H_n)\)。

**revert**：像 git revert，在当前之上新建 commit，内容回到祖先 \(H_{A^-}\)，不改写历史。

\[
\mathrm{rev}(A,A^{-})=A',\qquad H_{A'}=H_{A^{-}},\qquad \pi(A')=\{A\}
\]

**error commit**：Runtime 或 \(\Phi\) 失败仍落盘，并打 \(\mathrm{ok}=\bot\)。下一轮 \(\Gamma\) 看得到 \(o_\bot\)。

\[
\mathrm{err}(A,o_\bot)=A_\bot
\]

**branch / merge**：

\[
\mathrm{br}(\ell,A),\qquad
\mathrm{merge}(B_1,\ldots,B_k)=M,\qquad
H_M=\mu(H_{B_1},\ldots,H_{B_k}),\qquad
\pi(M)=\{B_1,\ldots,B_k\}
\]

\(\mu\) 可以调用 \(\Phi\)（带着多路观测）。谁和谁合、合完指向哪，是第二层的事；第一层仍然只转换 \(H\)。

```mermaid
flowchart TB
  subgraph L1["第一层 ℋ"]
    G["Γ = Φ ∘ Ω ∘ R"]
  end
  subgraph L2["第二层 ℛ"]
    FWD["fwd"]
    REV["rev"]
    ERR["err"]
    BR["br / merge"]
  end
  G --> FWD
  G --> ERR
  G --> BR
```
