(function () {
  "use strict";

  const TRIAL_PAGE = 100;

  const main = document.getElementById("main");
  const flash = document.getElementById("flash");

  let gen = 0;
  let jobsCache = [];
  let boardJobs = [];
  let compareSel = {};
  let chartInst = null;
  const chartState = { x: "step" };
  const BOARD_ORDER = ["peri-3142-full", "peri-3142-fail44"];

  const CHART_PALETTE = ["#2f9e44", "#f76707", "#7950f2", "#1c7ed6", "#e03131", "#0c8599", "#ae3ec9", "#7048e8"];
  const CHART_X = [
    { key: "step", label: "step", short: "步数", axis: "步数（向右更少）" },
    { key: "cost", label: "cost", short: "成本", axis: "成本（向右更少）" },
    { key: "token", label: "token", short: "Token", axis: "Token（向右更少）" }
  ];

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
    return escapeHtml(fmtTimePlain(s));
  }
  function fmtTimePlain(s) {
    if (s == null || s === "") return "—";
    if (typeof s === "number") {
      if (!Number.isFinite(s)) return "—";
      s = new Date(s).toISOString();
    }
    return String(s).replace("T", " ").replace("Z", " UTC");
  }
  function chartXDef(key) {
    for (let i = 0; i < CHART_X.length; i++) {
      if (CHART_X[i].key === key) return CHART_X[i];
    }
    return CHART_X[0];
  }
  function numOrZero(v) {
    const n = Number(v);
    return Number.isFinite(n) ? n : 0;
  }
  function tokenTotal(j) {
    return numOrZero(j.n_input_tokens) + numOrZero(j.n_cache_tokens) + numOrZero(j.n_output_tokens);
  }
  function chartXValue(j, xKey) {
    if (xKey === "cost") return numOrZero(j.cost_usd);
    if (xKey === "token") return tokenTotal(j);
    return numOrZero(j.n_agent_steps);
  }
  function fmtCost(v) {
    const n = Number(v);
    if (!Number.isFinite(n)) return "0";
    if (Number.isInteger(n)) return String(n);
    return n.toFixed(6).replace(/0+$/, "").replace(/\.$/, "");
  }
  function harnessOf(j) {
    return (j && j.agent_name) || "(harness 空)";
  }
  function modelOf(j) {
    return (j && j.model_name) || "(model 空)";
  }
  function harnessColorMap(items) {
    const names = [];
    const seen = {};
    items.forEach(function (j) {
      const a = harnessOf(j);
      if (!seen[a]) {
        seen[a] = true;
        names.push(a);
      }
    });
    names.sort();
    const map = {};
    names.forEach(function (a, i) {
      map[a] = CHART_PALETTE[i % CHART_PALETTE.length];
    });
    return map;
  }
  function modelHarnessPair(j) {
    return modelOf(j) + " · " + harnessOf(j);
  }
  function pointLabel(j, all) {
    const base = modelHarnessPair(j);
    let n = 0;
    for (let i = 0; i < all.length; i++) {
      if (harnessOf(all[i]) === harnessOf(j) && modelOf(all[i]) === modelOf(j)) n++;
    }
    if (n > 1) return base + "（" + (j.job_name || j.job_id) + "）";
    return base;
  }
  function boardKey(j) {
    const name = String((j && j.job_name) || "").trim();
    const lower = name.toLowerCase();
    if (lower.indexOf("peri-3142-fail44") !== -1) return "peri-3142-fail44";
    if (lower.indexOf("peri-3142-full") !== -1) return "peri-3142-full";
    return name || "(未命名作业)";
  }
  function boardName(j) {
    return boardKey(j);
  }
  function collectBoards(jobs) {
    const map = {};
    (jobs || []).forEach(function (j) {
      const k = boardKey(j);
      if (!map[k]) map[k] = { key: k, label: k, jobs: [] };
      map[k].jobs.push(j);
    });
    const keys = Object.keys(map);
    keys.sort(function (a, b) {
      const ia = BOARD_ORDER.indexOf(a);
      const ib = BOARD_ORDER.indexOf(b);
      if (ia !== -1 || ib !== -1) {
        if (ia === -1) return 1;
        if (ib === -1) return -1;
        return ia - ib;
      }
      return a.localeCompare(b, "zh-CN");
    });
    return keys.map(function (k) { return map[k]; });
  }
  function boardHash(key) {
    return "#/?board=" + encodeURIComponent(key);
  }
  function boardStats(jobs) {
    let nTrials = 0;
    let nRep = 0;
    let nUn = 0;
    let sum = 0;
    let nPass = 0;
    (jobs || []).forEach(function (j) {
      nTrials += numOrZero(j.n_trials);
      if (j.usage_reported) nRep += 1;
      else nUn += 1;
      const p = Number(j.pass_at_1);
      if (Number.isFinite(p)) {
        sum += p;
        nPass += 1;
      }
    });
    return {
      n_jobs: jobs.length,
      n_trials: nTrials,
      mean_pass_at_1: nPass ? sum / nPass : null,
      n_usage_reported_jobs: nRep,
      n_usage_unreported_jobs: nUn
    };
  }
  function boardTabsHTML(boards, selected) {
    if (!boards.length) return "";
    const btns = boards
      .map(function (b) {
        const on = b.key === selected;
        return (
          '<button type="button" class="board-tab" role="tab" data-board="' +
          escapeHtml(b.key) +
          '" aria-selected="' +
          (on ? "true" : "false") +
          '">' +
          escapeHtml(b.label) +
          '<span class="board-tab-n">' +
          b.jobs.length +
          "</span></button>"
        );
      })
      .join("");
    return (
      '<div class="board-tabs-wrap">' +
      '<div class="board-tabs" role="tablist" aria-label="榜单">' +
      btns +
      "</div>" +
      '<p class="board-tab-note">榜单名称取自 Harbor 作业名（job_name）。peri-3142-full 与 peri-3142-fail44 分列，不合并 Pass@1。</p>' +
      "</div>"
    );
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
    const qAt = raw.indexOf("?");
    const pathPart = qAt >= 0 ? raw.slice(0, qAt) : raw;
    const qs = qAt >= 0 ? new URLSearchParams(raw.slice(qAt + 1)) : new URLSearchParams();
    const parts = pathPart.split("/").filter(Boolean);
    const board = qs.get("board") || "";
    if (parts[0] === "compare") {
      return { view: "compare", jobA: parts[1] || "", jobB: parts[2] || "", board: board };
    }
    if (parts[0] === "jobs" && parts[1]) {
      return { view: "job", jobId: parts[1], trialId: parts[3] || "", board: board };
    }
    return { view: "board", board: board };
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
    const g = ++gen;
    if (!document.querySelector(".board-tabs")) {
      main.innerHTML = busy();
    }
    try {
      const list = await api("/v1/jobs?limit=200");
      if (g !== gen) return;
      showFlash("", false);
      jobsCache = list.items || [];
      const boards = collectBoards(jobsCache);
      let selected = parseRoute().board;
      if (!boards.some(function (b) { return b.key === selected; })) {
        selected = boards.length ? boards[0].key : "";
      }
      if (selected && parseRoute().board !== selected) {
        const next = boardHash(selected);
        if ((location.hash || "#/") !== next) {
          history.replaceState(null, "", next);
        }
      }
      boardJobs = selected
        ? jobsCache.filter(function (j) { return boardKey(j) === selected; })
        : [];
      const stats = boardStats(boardJobs);
      const mean = stats.mean_pass_at_1 == null ? "—" : fmtPct(stats.mean_pass_at_1);
      const rows = boardJobs
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
            '<td class="pick"><input class="chk" type="checkbox" data-cmp="' +
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
      const emptyMsg = jobsCache.length
        ? '<p class="empty">本榜尚无已 finalize 作业。</p>'
        : '<p class="empty">尚无已 finalize 且未隐藏的作业。请用 Harbor CLI 上传后再刷新。</p>';
      main.innerHTML =
        '<section class="banner"><h1>榜单</h1>' +
        "<p>每个标签页是一份独立榜单。一行一个 job_id。Pass@1 为该作业已入库 trial 的 reward 均值。下方均值为本榜各作业 Pass@1 的算术平均，不把不同作业的 trial 混池，也不把 peri-3142-full 与 peri-3142-fail44 合成一条。</p></section>" +
        boardTabsHTML(boards, selected) +
        '<div class="stats">' +
        '<div class="stat"><b>' +
        fmtNum(stats.n_jobs) +
        "</b><span>本榜作业</span></div>" +
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
        chartPanelHTML() +
        '<div class="filters">' +
        cmpBtn +
        "</div>" +
        (boardJobs.length
          ? '<div class="table-scroll"><table class="board"><thead><tr>' +
            "<th>对照</th><th>作业</th><th>agent / model</th><th>Pass@1</th><th>n</th>" +
            "<th>n_input_tokens</th><th>n_cache_tokens</th><th>n_output_tokens</th><th>cost_usd</th><th>n_agent_steps</th>" +
            "<th>签字</th><th>执行器</th></tr></thead><tbody>" +
            rows +
            "</tbody></table></div>"
          : emptyMsg);
      drawBoardChart();
    } catch (e) {
      if (g !== gen) return;
      showFlash(e.message || String(e), true);
      main.innerHTML = '<p class="error-box">' + escapeHtml(e.message || String(e)) + "</p>";
    }
  }

  function chartPanelHTML() {
    const xBtns = CHART_X.map(function (a) {
      const on = chartState.x === a.key;
      return (
        '<button type="button" class="chart-seg-btn" data-chart="x" data-x="' +
        escapeHtml(a.key) +
        '" aria-pressed="' +
        (on ? "true" : "false") +
        '">' +
        escapeHtml(a.label) +
        "</button>"
      );
    }).join("");
    return (
      '<section class="chart-panel" aria-labelledby="chart-title">' +
      '<header class="chart-head">' +
      '<h2 id="chart-title">完成度对照</h2>' +
      '<p class="chart-sub">完成度更高、消耗更少</p>' +
      '<div class="chart-controls">' +
      '<div class="chart-seg">' +
      '<span class="chart-seg-label" id="chart-x-label">X 轴</span>' +
      '<div class="chart-seg-btns" role="radiogroup" aria-labelledby="chart-x-label">' +
      xBtns +
      "</div></div>" +
      "</div>" +
      "</header>" +
      '<div class="chart-stage">' +
      '<div class="chart-plot">' +
      '<div class="chart-canvas-wrap" id="chart-wrap"></div>' +
      '<div id="chart-tip" class="chart-tip" hidden></div>' +
      "</div>" +
      '<aside class="chart-legend" id="chart-legend" aria-label="harness 图例"></aside>' +
      "</div>" +
      '<p class="chart-caption" id="chart-hint">Token = n_input_tokens + n_cache_tokens + n_output_tokens。横轴原始值向右更少，故右上更高完成度且更省消耗。每个点是一对 model · harness（harness 即 agent_name）。</p>' +
      "</section>"
    );
  }

  function setChartHint(text) {
    const el = document.getElementById("chart-hint");
    if (el) el.textContent = text || "";
  }

  function destroyBoardChart() {
    if (chartInst) {
      chartInst.destroy();
      chartInst = null;
    }
    const tip = document.getElementById("chart-tip");
    if (tip) {
      tip.hidden = true;
      tip.innerHTML = "";
    }
  }

  function jitterOffsets(points) {
    const groups = {};
    points.forEach(function (p, i) {
      const k = p.x + "\t" + p.y;
      if (!groups[k]) groups[k] = [];
      groups[k].push(i);
    });
    const dx = [];
    points.forEach(function () { dx.push(0); });
    Object.keys(groups).forEach(function (k) {
      const idxs = groups[k];
      if (idxs.length < 2) return;
      idxs.forEach(function (pi, n) {
        dx[pi] = (n - (idxs.length - 1) / 2) * 0.02;
      });
    });
    return dx;
  }

  function fillChartLegend(harnesses, colors) {
    const el = document.getElementById("chart-legend");
    if (!el) return;
    if (!harnesses || !harnesses.length) {
      el.innerHTML = "";
      el.hidden = true;
      return;
    }
    el.hidden = false;
    const rows = harnesses
      .map(function (h) {
        const color = (colors && colors[h]) || CHART_PALETTE[0];
        return (
          '<div class="chart-leg-row"><i style="background:' +
          color +
          '"></i>' +
          escapeHtml(h) +
          "</div>"
        );
      })
      .join("");
    el.innerHTML =
      rows +
      '<div class="chart-leg-row"><i class="hollow"></i>未报告用量</div>';
  }

  function renderChartTip(j, xDef, xVal, all) {
    const reported = !!j.usage_reported;
    const usageNote = reported
      ? '<p class="note" style="margin:0 0 8px">usage_reported=true</p>'
      : '<p class="unrep">未报告用量：下列用量为 0 表示源未给出，不是测得的 0。</p>';
    const ident = pointLabel(j, all || boardJobs);
    return (
      "<h3>详情</h3>" +
      '<p class="chart-tip-id">' +
      escapeHtml(ident) +
      "</p>" +
      '<dl class="kv">' +
      "<dt>榜单名称</dt><dd>" +
      escapeHtml(boardName(j)) +
      "</dd>" +
      (String(j.job_name || "") !== boardName(j)
        ? "<dt>作业</dt><dd>" + escapeHtml(j.job_name || "—") + "</dd>"
        : "") +
      "<dt>harness</dt><dd>" +
      escapeHtml(harnessOf(j)) +
      "</dd>" +
      "<dt>model</dt><dd>" +
      escapeHtml(modelOf(j)) +
      "</dd>" +
      "<dt>完成度</dt><dd>" +
      fmtPct(j.pass_at_1) +
      "（Pass@1 = " +
      fmtNum(j.n_reward_1) +
      "/" +
      fmtNum(j.n_trials) +
      "）</dd>" +
      "<dt>n_trials</dt><dd>" +
      fmtNum(j.n_trials) +
      "</dd>" +
      "<dt>" +
      escapeHtml(xDef.label) +
      "</dt><dd>" +
      (xDef.key === "cost" ? fmtCost(xVal) : fmtNum(xVal)) +
      "</dd>" +
      "</dl>" +
      usageNote +
      usageGrid(j)
    );
  }

  function chartTooltipExternal(context) {
    const tip = document.getElementById("chart-tip");
    const wrap = document.getElementById("chart-wrap");
    if (!tip || !wrap) return;
    const tooltip = context.tooltip;
    if (!tooltip || tooltip.opacity === 0 || !tooltip.dataPoints || !tooltip.dataPoints.length) {
      tip.hidden = true;
      return;
    }
    const pt = tooltip.dataPoints[0];
    const raw = pt.raw || {};
    const j = raw.job;
    if (!j) {
      tip.hidden = true;
      return;
    }
    const xDef = chartXDef(chartState.x);
    tip.innerHTML = renderChartTip(j, xDef, raw.xTrue != null ? raw.xTrue : pt.parsed.x, boardJobs);
    tip.hidden = false;
    const canvas = context.chart && context.chart.canvas;
    const plot = wrap.closest(".chart-plot") || wrap;
    if (!canvas) {
      tip.hidden = true;
      return;
    }
    const cr = canvas.getBoundingClientRect();
    const pr = plot.getBoundingClientRect();
    const caretX = tooltip.caretX;
    const caretY = tooltip.caretY;
    const leftRaw = cr.left - pr.left + caretX + 12;
    const maxLeft = Math.max(8, plot.clientWidth - tip.offsetWidth - 8);
    const left = Math.max(8, Math.min(leftRaw, maxLeft));
    let top = cr.top - pr.top + caretY - tip.offsetHeight - 12;
    if (top < 8) top = cr.top - pr.top + caretY + 16;
    const maxTop = Math.max(8, plot.clientHeight - tip.offsetHeight - 8);
    tip.style.left = left + "px";
    tip.style.top = Math.max(8, Math.min(top, maxTop)) + "px";
  }

  function drawBoardChart() {
    const wrap = document.getElementById("chart-wrap");
    if (!wrap) return;
    destroyBoardChart();
    if (typeof Chart === "undefined") {
      wrap.innerHTML = '<p class="chart-empty">对照图脚本未加载。</p>';
      fillChartLegend([]);
      setChartHint("");
      return;
    }
    const xDef = chartXDef(chartState.x);
    const items = boardJobs;
    if (!items.length) {
      wrap.innerHTML = jobsCache.length
        ? '<p class="chart-empty">本榜尚无已 finalize 作业。</p>'
        : '<p class="chart-empty">尚无已 finalize 作业，无法绘图。请用 Harbor CLI 上传后再刷新。</p>';
      fillChartLegend([]);
      setChartHint("");
      return;
    }
    const colors = harnessColorMap(items);
    const points = items.map(function (j) {
      const harness = harnessOf(j);
      return {
        job: j,
        harness: harness,
        label: pointLabel(j, items),
        x: chartXValue(j, xDef.key),
        y: numOrZero(j.pass_at_1),
        unreported: !j.usage_reported
      };
    });
    const jitter = jitterOffsets(points);
    const xSpan = Math.max.apply(
      null,
      points.map(function (p) { return p.x; }).concat([1])
    );
    const xPad = Math.max(xSpan, 1);
    const byHarness = {};
    points.forEach(function (p, i) {
      if (!byHarness[p.harness]) byHarness[p.harness] = [];
      const xPlot = p.x + jitter[i] * xPad;
      byHarness[p.harness].push({
        x: xPlot,
        y: p.y,
        xTrue: p.x,
        job: p.job,
        label: p.label,
        unreported: p.unreported
      });
    });
    const harnesses = Object.keys(byHarness).sort();
    const datasets = harnesses.map(function (harness) {
      const color = colors[harness] || CHART_PALETTE[0];
      const subset = byHarness[harness];
      return {
        label: harness,
        data: subset,
        showLine: false,
        borderColor: color,
        backgroundColor: subset.map(function (p) {
          return p.unreported ? "#ffffff" : color;
        }),
        pointBorderColor: subset.map(function (p) {
          return p.unreported ? color : "#ffffff";
        }),
        pointBackgroundColor: subset.map(function (p) {
          return p.unreported ? "#ffffff" : color;
        }),
        pointStyle: "circle",
        pointRadius: 8,
        pointHoverRadius: 10,
        pointHitRadius: 14,
        pointBorderWidth: 2
      };
    });
    fillChartLegend(harnesses, colors);
    wrap.innerHTML = '<canvas id="board-chart" role="img" aria-label="完成度对照散点图，右上更优"></canvas>';
    const canvas = document.getElementById("board-chart");
    const nUnrep = points.filter(function (p) { return p.unreported; }).length;
    let hint =
      "Token = n_input_tokens + n_cache_tokens + n_output_tokens。横轴原始值向右更少，故右上更高完成度且更省消耗。每个点是一对 model · harness（harness 即 agent_name）。已绘 " +
      points.length +
      " 个点、" +
      harnesses.length +
      " 种 harness 色。未报告用量的空心点落在 x=0（最右侧），不能当作实测零消耗。";
    if (nUnrep) {
      hint += " 空心点 " + nUnrep + " 个为 usage_reported=false。";
    }
    setChartHint(hint);
    Chart.defaults.font.family = '"Avenir Next", "Helvetica Neue", "PingFang SC", "Noto Sans SC", sans-serif';
    Chart.defaults.color = "#868e96";
    const betterZonePlugin = {
      id: "betterZone",
      beforeDatasetsDraw: function (chart) {
        const a = chart.chartArea;
        if (!a) return;
        const w = a.right - a.left;
        const h = a.bottom - a.top;
        const ctx = chart.ctx;
        ctx.save();
        ctx.fillStyle = "rgba(47, 158, 68, 0.12)";
        ctx.fillRect(a.right - w * 0.38, a.top, w * 0.38, h * 0.42);
        ctx.restore();
      }
    };
    chartInst = new Chart(canvas.getContext("2d"), {
      type: "scatter",
      data: { datasets: datasets },
      plugins: [betterZonePlugin],
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: false,
        layout: { padding: { left: 6, right: 8, top: 10, bottom: 4 } },
        interaction: { mode: "nearest", intersect: true, axis: "xy" },
        plugins: {
          legend: { display: false },
          tooltip: {
            enabled: false,
            external: chartTooltipExternal
          }
        },
        scales: {
          x: {
            type: "linear",
            reverse: true,
            title: { display: true, text: xDef.axis, color: "#868e96", padding: { top: 4 } },
            beginAtZero: true,
            grid: { color: "#ececec" },
            ticks: {
              color: "#868e96",
              callback: function (val) {
                return xDef.key === "cost" ? fmtCost(val) : fmtNum(val);
              }
            }
          },
          y: {
            type: "linear",
            title: { display: true, text: "完成度", color: "#868e96", padding: { bottom: 4 } },
            min: 0,
            suggestedMax: 1,
            grid: { color: "#ececec" },
            ticks: {
              color: "#868e96",
              callback: function (val) {
                return fmtPct(val);
              }
            }
          }
        }
      }
    });
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
    destroyBoardChart();
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
        kv("榜单名称", escapeHtml(boardName(job))) +
        kv("作业", escapeHtml(job.job_name || "—")) +
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
        kv("dataset_name", escapeHtml(job.dataset_name || "—"), true) +
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
    destroyBoardChart();
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
    const xBtn = e.target.closest("button[data-chart=\"x\"]");
    if (xBtn) {
      e.preventDefault();
      const v = xBtn.getAttribute("data-x");
      if (!v) return;
      chartState.x = v;
      main.querySelectorAll("button[data-chart=\"x\"]").forEach(function (b) {
        b.setAttribute("aria-pressed", b.getAttribute("data-x") === v ? "true" : "false");
      });
      drawBoardChart();
      return;
    }
    const tab = e.target.closest("button[data-board]");
    if (tab) {
      e.preventDefault();
      const key = tab.getAttribute("data-board");
      if (!key) return;
      const next = boardHash(key);
      if ((location.hash || "#/") === next) return;
      location.hash = next;
      return;
    }
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
  main.addEventListener("change", function (e) {
    const ctrl = e.target.closest("[data-chart]");
    if (!ctrl) return;
    if (ctrl.getAttribute("data-chart") === "x") {
      chartState.x = ctrl.value;
      drawBoardChart();
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
