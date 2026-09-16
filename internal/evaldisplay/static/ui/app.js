(function () {
  "use strict";

  const TRIAL_PAGE = 100;

  const main = document.getElementById("main");
  const flash = document.getElementById("flash");

  let gen = 0;
  let jobsCache = [];
  let compareSel = {};

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }
  function dash(v) {
    if (v == null || v === "") return "—";
    return String(v);
  }
  function fmtNum(n) {
    if (n == null || n === "") return "0";
    const x = Number(n);
    if (!Number.isFinite(x)) return "0";
    if (Number.isInteger(x)) return String(x);
    return String(x);
  }
  function fmtPct(x) {
    if (x == null || !Number.isFinite(Number(x))) return "—";
    return (Number(x) * 100).toFixed(1) + "%";
  }
  function fmtPass(n1, n, ratio) {
    if (!n) return "—";
    return escapeHtml(String(n1) + "/" + n) + " · " + fmtPct(ratio);
  }
  function fmtDur(sec) {
    if (sec == null || !Number.isFinite(Number(sec))) return "—";
    const s = Number(sec);
    if (s < 60) return s.toFixed(1) + " s";
    if (s < 3600) return (s / 60).toFixed(1) + " min";
    return (s / 3600).toFixed(1) + " h";
  }
  function fmtTime(s) {
    if (!s) return "—";
    return escapeHtml(String(s).replace("T", " ").replace("Z", " UTC"));
  }
  function usageMark(reported) {
    return reported ? "" : '<span class="unrep">未报告</span>';
  }
  function usageCell(label, value, reported) {
    return (
      '<div class="usage-cell"><span class="lbl">' +
      escapeHtml(label) +
      '</span><span class="num">' +
      fmtNum(value) +
      "</span>" +
      usageMark(reported) +
      "</div>"
    );
  }
  function usageGrid(obj) {
    const r = !!obj.usage_reported;
    return (
      '<div class="usage-grid">' +
      usageCell("n_input_tokens", obj.n_input_tokens, r) +
      usageCell("n_cache_tokens", obj.n_cache_tokens, r) +
      usageCell("n_output_tokens", obj.n_output_tokens, r) +
      usageCell("cost_usd", obj.cost_usd, r) +
      usageCell("n_agent_steps", obj.n_agent_steps, r) +
      "</div>"
    );
  }
  function showFlash(msg, on) {
    if (!on) {
      flash.hidden = true;
      flash.textContent = "";
      return;
    }
    flash.hidden = false;
    flash.textContent = msg;
  }
  function setNav(view) {
    document.querySelectorAll(".nav a").forEach(function (a) {
      if (a.getAttribute("data-nav") === view) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
  }
  function parseRoute() {
    const raw = (location.hash || "#/").replace(/^#/, "") || "/";
    const path = raw.split("?")[0];
    const parts = path.split("/").filter(Boolean);
    if (parts[0] === "compare") {
      return { view: "compare", jobA: parts[1] || "", jobB: parts[2] || "" };
    }
    if (parts[0] === "jobs" && parts[1]) {
      return { view: "job", jobId: parts[1], trialId: parts[3] || "" };
    }
    return { view: "board" };
  }
  class ApiError extends Error {
    constructor(status, message) {
      super(message);
      this.status = status;
    }
  }
  async function api(path) {
    const res = await fetch(path, {
      headers: {
        Accept: "application/json"
      }
    });
    if (!res.ok) {
      let msg = res.status + " " + res.statusText;
      try {
        const j = await res.json();
        if (j && j.error && j.error.message) msg = j.error.message;
      } catch (e) {}
      throw new ApiError(res.status, msg);
    }
    return res.json();
  }
  function busy() {
    return '<p class="busy">载入中…</p>';
  }

  async function renderBoard() {
    setNav("board");
    main.innerHTML = busy();
    const g = ++gen;
    try {
      const [stats, list] = await Promise.all([
        api("/v1/stats"),
        api("/v1/jobs?limit=200")
      ]);
      if (g !== gen) return;
      showFlash("", false);
      jobsCache = list.items || [];
      const mean = stats.mean_pass_at_1 == null ? "—" : fmtPct(stats.mean_pass_at_1);
      const rows = jobsCache
        .map(function (j) {
          const agent = escapeHtml(j.agent_name || "—") + (j.agent_version ? " / " + escapeHtml(j.agent_version) : "");
          const model = escapeHtml(j.model_name || "—") + (j.model_provider ? " · " + escapeHtml(j.model_provider) : "");
          const runner = [j.runner_name, j.runner_version].filter(Boolean).map(escapeHtml).join(" ") || "—";
          const inc = j.incomparability
            ? '<small title="' + escapeHtml(j.incomparability) + '">' + escapeHtml(j.incomparability) + "</small>"
            : "";
          const r = !!j.usage_reported;
          const checked = compareSel[j.job_id] ? " checked" : "";
          return (
            '<tr data-job="' +
            escapeHtml(j.job_id) +
            '" tabindex="0">' +
            '<td><input class="chk" type="checkbox" data-cmp="' +
            escapeHtml(j.job_id) +
            '"' +
            checked +
            " /></td>" +
            '<td class="cfg"><strong>' +
            escapeHtml(j.job_name) +
            "</strong><small><code title=\"" +
            escapeHtml(j.job_id) +
            '">' +
            escapeHtml(j.job_id) +
            "</code></small>" +
            inc +
            '<span class="bar" aria-hidden="true"><i style="width:' +
            Math.max(0, Math.min(100, Number(j.pass_at_1) * 100)) +
            '%"></i></span></td>' +
            "<td>" +
            agent +
            "<br><small>" +
            model +
            "</small></td>" +
            '<td class="num">' +
            fmtPass(j.n_reward_1, j.n_trials, j.pass_at_1) +
            "</td>" +
            '<td class="num">' +
            fmtNum(j.n_trials) +
            "</td>" +
            '<td class="num">' +
            fmtNum(j.n_input_tokens) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(j.n_cache_tokens) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(j.n_output_tokens) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(j.cost_usd) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(j.n_agent_steps) +
            usageMark(r) +
            "</td>" +
            "<td>" +
            escapeHtml(j.attestation_status || "unsigned") +
            "</td>" +
            "<td>" +
            runner +
            "</td>" +
            "</tr>"
          );
        })
        .join("");
      const ids = Object.keys(compareSel).filter(function (id) {
        return compareSel[id];
      });
      const cmpBtn =
        ids.length === 2
          ? '<a class="btn" href="#/compare/' +
            encodeURIComponent(ids[0]) +
            "/" +
            encodeURIComponent(ids[1]) +
            '">对照所选两条</a>'
          : '<span class="note" style="margin:0">勾选两条作业以对照；不会合并为一行。</span>';
      main.innerHTML =
        '<section class="banner"><h1>榜单</h1>' +
        "<p>一行一个 job_id。Pass@1 为该作业已入库 trial 的 reward 均值。下方均值为各作业 Pass@1 的算术平均，不把不同作业的 trial 混池。</p></section>" +
        '<div class="stats">' +
        '<div class="stat"><b>' +
        fmtNum(stats.n_jobs) +
        "</b><span>已 finalize 作业</span></div>" +
        '<div class="stat"><b>' +
        fmtNum(stats.n_trials) +
        "</b><span>已入库 trial</span></div>" +
        '<div class="stat"><b>' +
        mean +
        "</b><span>作业 Pass@1 均值</span></div>" +
        "</div>" +
        '<p class="note">用量已报告 ' +
        fmtNum(stats.n_usage_reported_jobs) +
        " 个作业；未报告 " +
        fmtNum(stats.n_usage_unreported_jobs) +
        " 个。标「未报告」的 0 表示源未给出，不是测得的零消耗。</p>" +
        '<div class="filters">' +
        cmpBtn +
        "</div>" +
        (jobsCache.length
          ? '<div class="table-scroll"><table class="board"><thead><tr>' +
            "<th></th><th>作业</th><th>agent / model</th><th>Pass@1</th><th>n</th>" +
            "<th>n_input_tokens</th><th>n_cache_tokens</th><th>n_output_tokens</th><th>cost_usd</th><th>n_agent_steps</th>" +
            "<th>签字</th><th>执行器</th></tr></thead><tbody>" +
            rows +
            "</tbody></table></div>"
          : '<p class="empty">尚无已 finalize 且未隐藏的作业。请用 Harbor CLI 上传后再刷新。</p>');
    } catch (e) {
      if (g !== gen) return;
      showFlash(e.message || String(e), true);
      main.innerHTML = '<p class="error-box">' + escapeHtml(e.message || String(e)) + "</p>";
    }
  }

  function kv(k, v, ours) {
    return "<dt" + (ours ? ' class="ours"' : "") + ">" + k + "</dt><dd>" + v + "</dd>";
  }
  function pretty(v) {
    if (v == null || v === "") return "—";
    try {
      return '<pre class="json">' + escapeHtml(JSON.stringify(v, null, 2)) + "</pre>";
    } catch (e) {
      return escapeHtml(String(v));
    }
  }
  function durStats(st) {
    if (!st || !st.n) return "无样本";
    return (
      "n=" +
      st.n +
      " · min " +
      fmtDur(st.min) +
      " · p50 " +
      fmtDur(st.p50) +
      " · mean " +
      fmtDur(st.mean) +
      " · max " +
      fmtDur(st.max)
    );
  }

  async function renderJob(jobId, trialId, trialPage) {
    setNav("board");
    main.innerHTML = busy();
    const g = ++gen;
    trialPage = trialPage || 0;
    try {
      const qs = new URLSearchParams();
      qs.set("limit", String(TRIAL_PAGE));
      qs.set("offset", String(trialPage * TRIAL_PAGE));
      qs.set("order", "task_name");
      const [job, agg, trials] = await Promise.all([
        api("/v1/jobs/" + encodeURIComponent(jobId)),
        api("/v1/jobs/" + encodeURIComponent(jobId) + "/aggregate"),
        api("/v1/jobs/" + encodeURIComponent(jobId) + "/trials?" + qs.toString())
      ]);
      if (g !== gen) return;
      showFlash("", false);
      let trial = null;
      if (trialId) {
        trial = await api("/v1/jobs/" + encodeURIComponent(jobId) + "/trials/" + encodeURIComponent(trialId));
        if (g !== gen) return;
      }
      const items = trials.items || [];
      const table = items
        .map(function (t) {
          const on = trial && t.trial_id === trial.trial_id ? " is-on" : "";
          const pass = Number(t.reward) === 1;
          const r = !!t.usage_reported;
          return (
            '<tr class="' +
            on.trim() +
            '" data-trial="' +
            escapeHtml(t.trial_id) +
            '" tabindex="0">' +
            "<td>" +
            escapeHtml(t.task_name) +
            '<br><small style="color:var(--faint)"><code>' +
            escapeHtml(t.trial_id) +
            "</code></small></td>" +
            '<td><span class="st ' +
            (pass ? "st-pass" : "st-fail") +
            '">' +
            (pass ? "通过" : "未通过") +
            "</span><br><span class=\"num\">reward=" +
            fmtNum(t.reward) +
            "</span></td>" +
            "<td>" +
            escapeHtml(t.exception_type || "—") +
            "</td>" +
            '<td class="num">' +
            fmtDur(t.duration_sec) +
            "</td>" +
            '<td class="num">' +
            fmtNum(t.n_input_tokens) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(t.n_cache_tokens) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(t.n_output_tokens) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(t.cost_usd) +
            usageMark(r) +
            "</td>" +
            '<td class="num">' +
            fmtNum(t.n_agent_steps) +
            usageMark(r) +
            "</td>" +
            "</tr>"
          );
        })
        .join("");
      const from = trials.total ? trialPage * TRIAL_PAGE + 1 : 0;
      const to = Math.min((trialPage + 1) * TRIAL_PAGE, trials.total || 0);
      const pager =
        '<div class="pager"><span>' +
        from +
        "–" +
        to +
        " / " +
        fmtNum(trials.total) +
        "</span>" +
        (trialPage > 0
          ? '<button type="button" class="btn" data-page="' + (trialPage - 1) + '">上一页</button>'
          : "") +
        (to < (trials.total || 0)
          ? '<button type="button" class="btn" data-page="' + (trialPage + 1) + '">下一页</button>'
          : "") +
        "</div>";
      let detail = '<p class="note">点击表格行查看该 trial 的标量与 rewards。v1 不提供 peri.txt / ATIF 会话。</p>';
      if (trial) {
        const tr = !!trial.usage_reported;
        detail =
          '<div class="facts">' +
          "<h4>Trial</h4><dl class=\"kv\">" +
          kv("trial_id", "<code>" + escapeHtml(trial.trial_id) + "</code>") +
          kv("task_name", escapeHtml(trial.task_name)) +
          kv("task_checksum", escapeHtml(trial.task_checksum || "—")) +
          kv("reward", fmtNum(trial.reward)) +
          kv("f2p", dash(trial.f2p)) +
          kv("p2p", dash(trial.p2p)) +
          kv("exception_type", escapeHtml(trial.exception_type || "—")) +
          kv("started_at", fmtTime(trial.started_at)) +
          kv("finished_at", fmtTime(trial.finished_at)) +
          kv("duration_sec", fmtDur(trial.duration_sec)) +
          kv("trajectory_uri", trial.trajectory_uri == null ? "null" : escapeHtml(String(trial.trajectory_uri))) +
          "</dl>" +
          usageGrid(trial) +
          (tr ? "" : '<p class="note">用量未报告：以上 0 不是测得的零消耗。</p>') +
          "<h4>verifier_rewards</h4>" +
          pretty(trial.verifier_rewards) +
          "<h4>agent_info</h4>" +
          pretty(trial.agent_info) +
          "<h4>exception_info</h4>" +
          pretty(trial.exception_info) +
          "<h4>environment_setup</h4>" +
          pretty(trial.environment_setup) +
          "<h4>agent_setup</h4>" +
          pretty(trial.agent_setup) +
          "<h4>agent_execution</h4>" +
          pretty(trial.agent_execution) +
          "<h4>verifier timing</h4>" +
          pretty(trial.verifier) +
          "</div>";
      }
      const phases = agg.phase_duration_sec || {};
      main.innerHTML =
        '<p class="crumb"><a href="#/">榜单</a> / ' +
        escapeHtml(job.job_name) +
        "</p>" +
        '<div class="run-id">' +
        "<h1>" +
        escapeHtml(job.job_name) +
        "</h1>" +
        '<dl class="kv">' +
        kv("job_id", "<code>" + escapeHtml(job.job_id) + "</code>") +
        kv("agent", escapeHtml(job.agent_name || "—") + (job.agent_version ? " / " + escapeHtml(job.agent_version) : "")) +
        kv("model", escapeHtml(job.model_name || "—") + (job.model_provider ? " · " + escapeHtml(job.model_provider) : "")) +
        kv("Pass@1", escapeHtml(agg.pass_at_1_fraction || fmtPass(agg.n_reward_1, agg.n_trials, agg.pass_at_1))) +
        kv("n_reward_1 / n_reward_0", fmtNum(agg.n_reward_1) + " / " + fmtNum(agg.n_reward_0)) +
        kv("墙钟", fmtDur(job.duration_sec)) +
        kv("duration 分位", durStats(agg.duration_sec)) +
        kv("environment_setup", durStats(phases.environment_setup)) +
        kv("agent_setup", durStats(phases.agent_setup)) +
        kv("agent_execution", durStats(phases.agent_execution)) +
        kv("verifier", durStats(phases.verifier)) +
        "</dl>" +
        '<dl class="kv">' +
        kv("签字", escapeHtml(job.attestation_status || "unsigned") + (job.attestor ? " · " + escapeHtml(job.attestor) : ""), true) +
        kv("执行器", dash([job.runner_name, job.runner_version].filter(Boolean).join(" ")), true) +
        kv("endpoint_class", escapeHtml(job.endpoint_class || "—"), true) +
        kv("job_type", escapeHtml(job.job_type || "—"), true) +
        kv("dataset_ref", escapeHtml(job.dataset_ref || "—"), true) +
        kv("dataset_path", escapeHtml(job.dataset_path || "—"), true) +
        kv("listed", job.listed === false ? "否" : "是", true) +
        kv("不可比说明", escapeHtml(job.incomparability || "—"), true) +
        "</dl></div>" +
        (job.usage_reported
          ? ""
          : '<p class="note">该作业 usage_reported=false：下列用量为 0 表示源未报告，不是测得的零消耗。</p>') +
        usageGrid(agg) +
        '<div class="run-grid">' +
        '<div class="pane"><h3>Trial 详情</h3><div class="pane-box">' +
        detail +
        "</div></div>" +
        '<div class="pane"><h3>Trials</h3><div class="pane-box trials-wrap"><table class="trials"><thead><tr>' +
        "<th>task_name</th><th>reward</th><th>exception</th><th>duration</th>" +
        "<th>n_input_tokens</th><th>n_cache_tokens</th><th>n_output_tokens</th><th>cost_usd</th><th>n_agent_steps</th>" +
        "</tr></thead><tbody>" +
        table +
        "</tbody></table></div>" +
        pager +
        "</div></div>";
      main.dataset.jobId = jobId;
      main.dataset.trialPage = String(trialPage);
    } catch (e) {
      if (g !== gen) return;
      showFlash(e.message || String(e), true);
      main.innerHTML = '<p class="error-box">' + escapeHtml(e.message || String(e)) + "</p>";
    }
  }

  async function renderCompare(jobA, jobB) {
    setNav("compare");
    main.innerHTML = busy();
    const g = ++gen;
    try {
      if (!jobsCache.length) {
        const list = await api("/v1/jobs?limit=200");
        jobsCache = list.items || [];
      }
      const opts = jobsCache
        .map(function (j) {
          return (
            '<option value="' +
            escapeHtml(j.job_id) +
            '">' +
            escapeHtml(j.job_name) +
            " · " +
            escapeHtml(j.job_id) +
            "</option>"
          );
        })
        .join("");
      if (!jobA || !jobB) {
        if (g !== gen) return;
        main.innerHTML =
          '<section class="banner"><h1>对照</h1><p>选择两个 job_id。结果并排两列，不会合成一条榜。</p></section>' +
          '<form class="compare-pick" id="cmp-form">' +
          "<label>作业 A<select name=\"a\">" +
          opts +
          "</select></label>" +
          "<label>作业 B<select name=\"b\">" +
          opts +
          "</select></label>" +
          '<button class="btn" type="submit">对照</button></form>';
        return;
      }
      const cmp = await api(
        "/v1/compare?job_a=" + encodeURIComponent(jobA) + "&job_b=" + encodeURIComponent(jobB)
      );
      if (g !== gen) return;
      showFlash("", false);
      function card(side, agg) {
        return (
          '<div class="compare-card"><h2>' +
          escapeHtml(agg.job_name || side) +
          "</h2><dl class=\"kv\">" +
          kv("job_id", "<code>" + escapeHtml(agg.job_id) + "</code>") +
          kv("Pass@1", escapeHtml(agg.pass_at_1_fraction || fmtPass(agg.n_reward_1, agg.n_trials, agg.pass_at_1))) +
          kv("n_trials", fmtNum(agg.n_trials)) +
          kv("agent", escapeHtml(agg.agent_name || "—")) +
          kv("model", escapeHtml(agg.model_name || "—")) +
          kv("duration", durStats(agg.duration_sec)) +
          "</dl>" +
          usageGrid(agg) +
          (agg.usage_reported ? "" : '<p class="note">用量未报告。</p>') +
          '<p><a href="#/jobs/' +
          encodeURIComponent(agg.job_id) +
          '">查看作业</a></p></div>'
        );
      }
      main.innerHTML =
        '<section class="banner"><h1>对照</h1>' +
        "<p>dataset_match=" +
        (cmp.dataset_match ? "true" : "false") +
        " · checksum 交集 overlap_n=" +
        fmtNum(cmp.overlap_n) +
        "。两行仍是独立作业。</p></section>" +
        '<form class="compare-pick" id="cmp-form">' +
        "<label>作业 A<select name=\"a\">" +
        opts +
        '</select></label>' +
        "<label>作业 B<select name=\"b\">" +
        opts +
        "</select></label>" +
        '<button class="btn" type="submit">对照</button></form>' +
        '<div class="compare-grid">' +
        card("A", cmp.job_a) +
        card("B", cmp.job_b) +
        "</div>";
      const form = document.getElementById("cmp-form");
      if (form) {
        form.a.value = jobA;
        form.b.value = jobB;
      }
    } catch (e) {
      if (g !== gen) return;
      showFlash(e.message || String(e), true);
      main.innerHTML = '<p class="error-box">' + escapeHtml(e.message || String(e)) + "</p>";
    }
  }

  function route() {
    const r = parseRoute();
    if (r.view === "compare") return renderCompare(r.jobA, r.jobB);
    if (r.view === "job") return renderJob(r.jobId, r.trialId, 0);
    return renderBoard();
  }

  window.addEventListener("hashchange", route);
  main.addEventListener("click", function (e) {
    const chk = e.target.closest("input[data-cmp]");
    if (chk) {
      e.stopPropagation();
      compareSel[chk.getAttribute("data-cmp")] = chk.checked;
      return;
    }
    const jobRow = e.target.closest("tr[data-job]");
    if (jobRow && !e.target.closest("input")) {
      location.hash = "#/jobs/" + jobRow.getAttribute("data-job");
      return;
    }
    const trialRow = e.target.closest("tr[data-trial]");
    if (trialRow && main.dataset.jobId) {
      location.hash =
        "#/jobs/" + main.dataset.jobId + "/trials/" + trialRow.getAttribute("data-trial");
      return;
    }
    const page = e.target.closest("[data-page]");
    if (page && main.dataset.jobId) {
      renderJob(main.dataset.jobId, parseRoute().trialId, Number(page.getAttribute("data-page")));
    }
  });
  main.addEventListener("submit", function (e) {
    const form = e.target.closest("#cmp-form");
    if (!form) return;
    e.preventDefault();
    const a = form.a.value;
    const b = form.b.value;
    if (!a || !b || a === b) {
      showFlash("请选择两个不同的 job_id。", true);
      return;
    }
    location.hash = "#/compare/" + encodeURIComponent(a) + "/" + encodeURIComponent(b);
  });
  main.addEventListener("keydown", function (e) {
    if (e.key !== "Enter" && e.key !== " ") return;
    const row = e.target.closest("tr[data-job], tr[data-trial]");
    if (!row) return;
    e.preventDefault();
    row.click();
  });
  route();
})();
