(function () {
  "use strict";

  const TOKEN_KEY = "eval_display_admin_token";
  const main = document.getElementById("main");
  const flash = document.getElementById("flash");
  const tokenInput = document.getElementById("token-input");
  const tokenForm = document.getElementById("token-form");
  let gen = 0;

  function getToken() {
    return sessionStorage.getItem(TOKEN_KEY) || "";
  }
  function setToken(t) {
    t = String(t || "").trim().replace(/^bearer\s+/i, "");
    sessionStorage.setItem(TOKEN_KEY, t);
  }
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
    return escapeHtml(String(v));
  }
  function fmtNum(n) {
    if (n == null || n === "") return "0";
    const x = Number(n);
    if (!Number.isFinite(x)) return "0";
    return String(x);
  }
  function fmtPct(x) {
    if (x == null || !Number.isFinite(Number(x))) return "—";
    return (Number(x) * 100).toFixed(1) + "%";
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
    document.querySelectorAll(".nav a[data-nav]").forEach(function (a) {
      if (a.getAttribute("data-nav") === view) a.setAttribute("aria-current", "page");
      else a.removeAttribute("aria-current");
    });
  }
  class AuthError extends Error {
    constructor(status, message) {
      super(message || "auth");
      this.status = status || 401;
    }
  }
  class ApiError extends Error {
    constructor(status, message) {
      super(message);
      this.status = status;
    }
  }
  async function api(path, opts) {
    opts = opts || {};
    const headers = Object.assign(
      {
        Authorization: "Bearer " + getToken(),
        Accept: "application/json"
      },
      opts.headers || {}
    );
    if (opts.body && !headers["Content-Type"]) headers["Content-Type"] = "application/json";
    const res = await fetch(path, {
      method: opts.method || "GET",
      headers: headers,
      body: opts.body || undefined
    });
    if (res.status === 401 || res.status === 403) {
      throw new AuthError(res.status, res.status === 403 ? "非后台令牌不能调用管理接口。" : "后台令牌无效或未填写。");
    }
    if (res.status === 204) return {};
    let data = null;
    try {
      data = await res.json();
    } catch (e) {
      data = null;
    }
    if (!res.ok) {
      const msg = data && data.error && data.error.message ? data.error.message : res.statusText;
      throw new ApiError(res.status, msg);
    }
    return data;
  }
  function parseRoute() {
    const raw = (location.hash || "#/").replace(/^#/, "") || "/";
    const parts = raw.split("/").filter(Boolean);
    if (parts[0] === "health") return { view: "health" };
    if (parts[0] === "jobs" && parts[1]) return { view: "job", jobId: parts[1] };
    return { view: "jobs" };
  }

  async function renderHealth() {
    setNav("health");
    main.innerHTML = '<p class="busy">载入中…</p>';
    const g = ++gen;
    try {
      const st = await api("/v1/admin/status");
      if (g !== gen) return;
      showFlash("", false);
      main.innerHTML =
        "<h1>健康</h1>" +
        '<p class="note">不返回密钥或 JWT。</p>' +
        '<div class="stats">' +
        '<div class="stat"><b>' +
        escapeHtml(st.status || "—") +
        "</b><span>status</span></div>" +
        '<div class="stat"><b>' +
        (st.ready ? "true" : "false") +
        "</b><span>ready</span></div>" +
        '<div class="stat"><b>' +
        fmtNum(st.n_hub_jobs) +
        "</b><span>hub_job</span></div>" +
        '<div class="stat"><b>' +
        fmtNum(st.n_finalized_jobs) +
        "</b><span>已 finalize</span></div>" +
        "</div>" +
        '<dl class="kv">' +
        "<dt>sqlite_path</dt><dd><code>" +
        escapeHtml(st.sqlite_path || "—") +
        "</code></dd>" +
        "<dt>s3_backend</dt><dd>" +
        escapeHtml(st.s3_backend || "—") +
        "</dd></dl>";
    } catch (e) {
      handleErr(g, e);
    }
  }

  async function renderJobs() {
    setNav("jobs");
    main.innerHTML = '<p class="busy">载入中…</p>';
    const g = ++gen;
    try {
      const list = await api("/v1/admin/jobs?limit=200");
      if (g !== gen) return;
      showFlash("", false);
      const items = list.items || [];
      const rows = items
        .map(function (j) {
          const pass = j.finalized ? fmtPct(j.pass_at_1) : "—";
          return (
            '<tr data-job="' +
            escapeHtml(j.job_id) +
            '"><td class="cfg"><strong>' +
            escapeHtml(j.job_name || "—") +
            '</strong><br><code>' +
            escapeHtml(j.job_id) +
            "</code></td><td>" +
            escapeHtml(j.ingest_status) +
            "</td><td>" +
            (j.listed ? "是" : "否") +
            "</td><td>" +
            escapeHtml(j.attestation_status || "—") +
            "</td><td class=\"num\">" +
            pass +
            "</td><td class=\"num\">" +
            fmtNum(j.n_hub_trials) +
            " / " +
            fmtNum(j.n_trials) +
            "</td><td>" +
            dash(j.job_type) +
            "</td><td>" +
            dash(j.endpoint_class) +
            "</td></tr>"
          );
        })
        .join("");
      main.innerHTML =
        "<h1>作业</h1>" +
        '<p class="note">含尚未 finalize 的 hub_job。一行一个 job_id。点击进入 overlay / 签字 / 删除。</p>' +
        (items.length
          ? '<div class="table-scroll"><table><thead><tr>' +
            "<th>作业</th><th>ingest</th><th>前台可见</th><th>签字</th><th>Pass@1</th><th>hub / 分析 trial</th><th>job_type</th><th>endpoint_class</th>" +
            "</tr></thead><tbody>" +
            rows +
            "</tbody></table></div>"
          : '<p class="empty">尚无 hub_job。</p>');
    } catch (e) {
      handleErr(g, e);
    }
  }

  function field(name, label, value, type) {
    type = type || "text";
    if (type === "textarea") {
      return (
        '<label class="wide">' +
        escapeHtml(label) +
        '<textarea name="' +
        name +
        '">' +
        escapeHtml(value || "") +
        "</textarea></label>"
      );
    }
    if (type === "select") {
      return (
        '<label>' +
        escapeHtml(label) +
        '<select name="' +
        name +
        '">' +
        value +
        "</select></label>"
      );
    }
    return (
      '<label>' +
      escapeHtml(label) +
      '<input name="' +
      name +
      '" type="' +
      type +
      '" value="' +
      escapeHtml(value || "") +
      '" /></label>'
    );
  }
  function opt(cur, v, label) {
    return (
      '<option value="' +
      escapeHtml(v) +
      '"' +
      (cur === v ? " selected" : "") +
      ">" +
      escapeHtml(label) +
      "</option>"
    );
  }

  async function renderJob(jobId) {
    setNav("jobs");
    main.innerHTML = '<p class="busy">载入中…</p>';
    const g = ++gen;
    try {
      const j = await api("/v1/admin/jobs/" + encodeURIComponent(jobId));
      if (g !== gen) return;
      showFlash("", false);
      const jt = j.job_type == null ? "" : j.job_type;
      const listed = j.listed !== false;
      const att = j.attestation_status || "unsigned";
      main.innerHTML =
        '<p class="note"><a href="#/">全部作业</a></p>' +
        "<h1>" +
        escapeHtml(j.job_name || jobId) +
        "</h1>" +
        '<dl class="kv">' +
        "<dt>job_id</dt><dd><code>" +
        escapeHtml(j.job_id) +
        "</code></dd>" +
        "<dt>ingest_status</dt><dd>" +
        escapeHtml(j.ingest_status) +
        "</dd>" +
        "<dt>archive_path</dt><dd>" +
        dash(j.archive_path) +
        "</dd>" +
        "<dt>hub / 分析 trial</dt><dd>" +
        fmtNum(j.n_hub_trials) +
        " / " +
        fmtNum(j.n_trials) +
        "</dd>" +
        "<dt>Pass@1</dt><dd>" +
        (j.finalized ? fmtPct(j.pass_at_1) + "（" + fmtNum(j.n_reward_1) + "/" + fmtNum(j.n_trials) + "）" : "尚未 finalize") +
        "</dd></dl>" +
        (j.finalized
          ? '<form id="ov-form" class="form-grid">' +
            field(
              "job_type",
              "job_type",
              opt(jt, "", "（空）") + opt(jt, "H", "H") + opt(jt, "M", "M"),
              "select"
            ) +
            field("endpoint_class", "endpoint_class", j.endpoint_class || "") +
            field(
              "attestation_status",
              "attestation",
              opt(att, "unsigned", "unsigned") + opt(att, "signed", "signed"),
              "select"
            ) +
            field("attestor", "attestor", j.attestor || "") +
            field(
              "listed",
              "前台可见 listed",
              opt(listed ? "true" : "false", "true", "是") + opt(listed ? "true" : "false", "false", "否"),
              "select"
            ) +
            field("dataset_ref", "dataset_ref", j.dataset_ref || "") +
            field("dataset_path", "dataset_path", j.dataset_path || "") +
            field("incomparability", "incomparability", j.incomparability || "", "textarea") +
            '<div class="wide actions"><button class="btn" type="submit">保存 overlay</button></div></form>'
          : '<p class="note">尚未写入分析表，无法编辑 overlay。可删除后由 CLI 重试上传。</p>') +
        '<div class="confirm"><p class="note" style="margin-bottom:8px">删除将去掉分析行、hub_job 及对应 S3 前缀，不可恢复。</p>' +
        '<form id="del-form" class="actions">' +
        '<label>输入 job_id 确认 <input name="confirm" autocomplete="off" /></label>' +
        '<button class="btn btn-danger" type="submit">删除作业</button></form></div>';
      const ov = document.getElementById("ov-form");
      if (ov) {
        ov.addEventListener("submit", async function (e) {
          e.preventDefault();
          const fd = new FormData(ov);
          const jobType = fd.get("job_type");
          const body = {
            job_type: jobType === "" ? null : jobType,
            endpoint_class: fd.get("endpoint_class") || null,
            attestation: {
              status: fd.get("attestation_status"),
              attestor: fd.get("attestor") || ""
            },
            listed: fd.get("listed") === "true",
            dataset_ref: fd.get("dataset_ref") || null,
            dataset_path: fd.get("dataset_path") || null,
            incomparability: fd.get("incomparability") || null
          };
          try {
            await api("/v1/jobs/" + encodeURIComponent(jobId) + "/overlay", {
              method: "PUT",
              body: JSON.stringify(body)
            });
            showFlash("", false);
            renderJob(jobId);
          } catch (err) {
            showFlash(err.message || String(err), true);
          }
        });
      }
      const del = document.getElementById("del-form");
      if (del) {
        del.addEventListener("submit", async function (e) {
          e.preventDefault();
          const typed = String(new FormData(del).get("confirm") || "").trim();
          if (typed !== jobId) {
            showFlash("确认框须原样填入 job_id。", true);
            return;
          }
          try {
            await api("/v1/admin/jobs/" + encodeURIComponent(jobId), { method: "DELETE" });
            location.hash = "#/";
          } catch (err) {
            showFlash(err.message || String(err), true);
          }
        });
      }
    } catch (e) {
      handleErr(g, e);
    }
  }

  function handleErr(g, e) {
    if (g !== gen) return;
    if (e instanceof AuthError) {
      showFlash(e.message, true);
      main.innerHTML =
        "<h1>需要后台令牌</h1><p class=\"note\">请在顶栏填入 EVAL_DISPLAY_ADMIN_TOKEN。前台公开可读，管理接口仅接受后台令牌。</p>";
      return;
    }
    showFlash(e.message || String(e), true);
    main.innerHTML = '<p class="error-box">' + escapeHtml(e.message || String(e)) + "</p>";
  }

  function route() {
    const r = parseRoute();
    if (r.view === "health") return renderHealth();
    if (r.view === "job") return renderJob(r.jobId);
    return renderJobs();
  }

  tokenInput.value = getToken();
  tokenForm.addEventListener("submit", function (e) {
    e.preventDefault();
    setToken(tokenInput.value);
    tokenInput.value = getToken();
    route();
  });
  window.addEventListener("hashchange", route);
  main.addEventListener("click", function (e) {
    const row = e.target.closest("tr[data-job]");
    if (row && !e.target.closest("form")) {
      location.hash = "#/jobs/" + row.getAttribute("data-job");
    }
  });
  route();
})();
