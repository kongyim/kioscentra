import {
  AdminAPI,
  APIError,
  escapeHTML as e,
  validID,
  dateTime,
  relativeTime,
  bytes,
  percent,
  uptime,
  reportName,
} from "./core.js";

const icon = (name) => {
  const paths = {
    grid: '<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
    monitor:
      '<rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 21h8m-4-4v4"/>',
    refresh:
      '<path d="M20 7v5h-5M4 17v-5h5M6 7a7 7 0 0 1 12-2l2 3M4 16l2 3a7 7 0 0 0 12-2"/>',
    arrow: '<path d="m9 5 7 7-7 7"/>',
    search: '<circle cx="10" cy="10" r="6"/><path d="m15 15 5 5"/>',
    back: '<path d="m14 6-6 6 6 6M8 12h13"/>',
    exit: '<path d="M9 4H4v16h5m5-12 4 4-4 4m-6-4h11"/>',
    pulse: '<path d="M2 12h5l3-8 4 16 3-8h5"/>',
    shield:
      '<path d="M12 3 4 6v6c0 5 8 9 8 9s8-4 8-9V6l-8-3Z"/><path d="m8 12 3 3 5-6"/>',
  };
  return `<svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${paths[name] ?? paths.monitor}</svg>`;
};
const logo = `<span class="logo-mark">k<span>·</span></span><span>Kiosentra<small>CENTRAL CONSOLE</small></span>`;
const badge = (online) =>
  `<span class="badge ${online ? "online" : "offline"}"><i></i>${online ? "在線" : "離線"}</span>`;
const empty = (title, description) =>
  `<div class="empty">${icon("monitor")}<h3>${e(title)}</h3><p>${e(description)}</p></div>`;
const field = (label, value) =>
  `<div class="field"><dt>${e(label)}</dt><dd>${e(value ?? "—")}</dd></div>`;
const count = (value) => Number(value ?? 0).toLocaleString("zh-HK");
const message = (error) =>
  error instanceof APIError
    ? error.message
    : "連線暫時中斷，請確認中央 Server 正常運作後重試。";

export function mount(
  root,
  { apiFactory = (token) => new AdminAPI(token), window: win = window } = {},
) {
  let api = null,
    generation = 0,
    pending = null,
    loading = false,
    error = "",
    lastUpdated = null;
  let devices = null,
    detail = null,
    reports = null,
    currentID = "",
    search = "",
    draft = "",
    status = "",
    cursor = "",
    history = [];
  let jobs = null, jobCursor = "", jobHistory = [], selectedJob = null, selectedJobID = "";
  let eventCursor = "", eventHistory = [];
  let jobVersion = "", jobScenario = "success", jobLease = "300", mutationNote = "";
  const mutationKeys = new Map();
  let reportCursor = "",
    reportHistory = [],
    auto = true;
  const abort = () => {
    generation++;
    pending?.abort();
    pending = null;
    loading = false;
  };
  function login(note = "") {
    win.document.title = "連接主控台 · Kiosentra";
    root.innerHTML = `<main class="login" id="content"><section class="welcome"><div class="brand">${logo}</div><div class="welcome-copy"><span class="eyebrow">DEVICE OPERATIONS</span><h1>每一部裝置，<br>一目了然。</h1><p>由連線狀態到系統盤點，<br>喺同一個地方掌握現場運作。</p><div class="connection-art" aria-hidden="true"><div>${icon("monitor")}</div><span></span><div class="hub">k·</div><span></span><div>${icon("pulse")}</div></div></div><div class="welcome-footer">Kiosentra <span>裝置管理主控台</span></div></section><section class="login-panel"><div class="login-form"><span class="eyebrow">歡迎返嚟</span><h2>連接管理主控台</h2><p class="muted">使用管理員 token 查看裝置及管理模擬工作。</p><form id="login-form"><label for="token">管理員 token</label><input id="token" name="token" type="password" autocomplete="off" spellcheck="false" required placeholder="輸入管理員 token"><p class="form-note">Token 只會保留喺今次頁面工作階段。</p><div id="login-error" class="error-text" role="alert">${e(note)}</div><button class="primary login-button" type="submit">連接主控台 ${icon("arrow")}</button></form><div class="endpoint">${icon("shield")}<span>連接至 <strong>${e(win.location.host)}</strong></span></div></div></section></main>`;
    root
      .querySelector("#login-form")
      .addEventListener("submit", async (event) => {
        event.preventDefault();
        const input = root.querySelector("#token");
        const token = input.value.trim();
        if (!token) return;
        input.value = "";
        const button = root.querySelector("button");
        button.disabled = true;
        button.textContent = "連接中…";
        const candidate = apiFactory(token);
        const ticket = ++generation;
        pending = new AbortController();
        try {
          const page = await candidate.devices({ signal: pending.signal });
          if (ticket !== generation) {
            candidate.clear();
            return;
          }
          api = candidate;
          devices = page;
          lastUpdated = new Date();
          pending = null;
          shell();
          route();
        } catch (err) {
          candidate.clear();
          if (ticket === generation) {
            pending = null;
            login(message(err));
            root.querySelector("#token").focus();
          }
        }
      });
  }
  function logout(note = "") {
    abort();
    api?.clear();
    api = null;
    devices = detail = reports = jobs = selectedJob = null;
    jobCursor = selectedJobID = jobVersion = mutationNote = ""; jobHistory = []; eventCursor = ""; eventHistory = []; mutationKeys.clear();
    search = draft = status = cursor = currentID = "";
    history = [];
    reportHistory = [];
    reportCursor = "";
    lastUpdated = null;
    error = "";
    win.history.replaceState(null, "", "#/devices");
    login(note);
  }
  function shell() {
    root.innerHTML = `<div class="workspace"><aside class="sidebar"><a class="brand" href="#/devices" aria-label="Kiosentra 裝置總覽">${logo}</a><div class="nav-label">工作空間</div><nav><a href="#/devices" class="nav-item">${icon("grid")}裝置總覽<span class="nav-dot"></span></a></nav><div class="sidebar-bottom"><div class="server-label"><span class="dot"></span>中央 Server</div><div class="server-host">${e(win.location.host)}</div><div class="session"><span class="avatar">管</span><div>管理員<small>模擬工作管理</small></div><button class="icon-button" id="logout" title="登出" aria-label="登出">${icon("exit")}</button></div></div></aside><div class="main-wrap"><header class="topbar"><div>工作空間 <span>/</span> <strong>裝置管理</strong></div><span class="secure-label">${icon("shield")} 管理員已連接</span></header><main id="content" tabindex="-1"><div id="view"></div></main><footer class="page-footer"><span>Kiosentra · Central</span><span>裝置連線以最近 90 秒收到嘅上報為準</span></footer></div></div>`;
    root.querySelector("#logout").onclick = () => logout();
  }
  function render() {
    const view = root.querySelector("#view");
    if (!view) return;
    win.document.title = currentID
      ? `${detail?.hostname ?? "裝置詳情"} · Kiosentra`
      : "裝置總覽 · Kiosentra";
    view.setAttribute("aria-busy", String(loading));
    const controls = `<div class="page-actions"><label class="auto"><input id="auto" type="checkbox" ${auto ? "checked" : ""}>自動更新</label><button class="button" id="refresh" ${loading ? "disabled" : ""}>${icon("refresh")}${loading ? "更新中…" : "重新整理"}</button></div>`;
    const banner = error
      ? `<div class="error-banner" role="alert">${e(error)}${lastUpdated ? " 顯示嘅係上次成功讀取嘅資料。" : ""}</div>`
      : "";
    const updated = `<span class="updated" role="status">${lastUpdated ? `更新於 ${e(dateTime(lastUpdated))}` : "讀取資料中…"}</span>`;
    view.innerHTML = currentID
      ? `${banner}<a class="back-link" href="#/devices">${icon("back")}返回裝置列表</a><div class="page-heading"><div><span class="eyebrow">DEVICE DETAIL</span><h1>${e(detail?.hostname ?? "裝置詳情")} ${detail ? badge(detail.online) : ""}</h1>${updated}</div>${controls}</div>${detail ? detailView() : loading ? empty("正在讀取裝置", "整理最新資料…") : empty("未能載入裝置", "請重試或返回裝置列表。")}`
      : `${banner}<div class="page-heading"><div><span class="eyebrow">FLEET OVERVIEW</span><h1>裝置總覽<span class="heading-dot">.</span></h1><p class="muted">掌握現場連線，追蹤每一部裝置。</p></div>${controls}</div>${listView(updated)}`;
    view.querySelector("#refresh").onclick = () => load();
    view.querySelector("#auto").onchange = (event) => {
      auto = event.target.checked;
    };
    if (!currentID) {
      view.querySelector("#search").oninput = (event) => {
        draft = event.target.value;
      };
      view.querySelector("#search-form").onsubmit = (event) => {
        event.preventDefault();
        search = draft.trim();
        cursor = "";
        history = [];
        load();
      };
      view.querySelector("#status").onchange = (event) => {
        status = event.target.value;
        cursor = "";
        history = [];
        load();
      };
      view.querySelector("#next").onclick = () => {
        if (devices?.nextCursor) {
          history.push(cursor);
          cursor = devices.nextCursor;
          load();
        }
      };
      view.querySelector("#previous").onclick = () => {
        cursor = history.pop() ?? "";
        load();
      };
    } else if (detail) {
      bindJobs(view);
      view.querySelector("#older").onclick = () => {
        if (reports?.nextCursor) {
          reportHistory.push(reportCursor);
          reportCursor = reports.nextCursor;
          load();
        }
      };
      view.querySelector("#newer").onclick = () => {
        reportCursor = reportHistory.pop() ?? "";
        load();
      };
      const softwareSearch = view.querySelector("#software-search");
      softwareSearch.oninput = () => {
        const q = softwareSearch.value.toLocaleLowerCase();
        let visible = 0;
        for (const row of view.querySelectorAll("[data-software]")) {
          row.hidden = !row.textContent.toLocaleLowerCase().includes(q);
          if (!row.hidden) visible++;
        }
        view.querySelector("#software-count").textContent = `${visible} 項`;
      };
    }
  }
  function listView(updated) {
    const rows = devices?.items ?? [];
    return `<section class="stats" aria-label="裝置統計"><div class="stat"><div>裝置總數 <span>${icon("monitor")}</span></div><strong>${devices ? count(devices.total) : "—"}</strong><small>${search ? "符合搜尋嘅裝置" : "已註冊裝置"}</small></div><div class="stat"><div><span class="dot"></span>在線</div><strong>${devices ? count(devices.online) : "—"}<small>部</small></strong><small>最近 90 秒內收到上報</small></div><div class="stat"><div><span class="dot grey"></span>離線</div><strong>${devices ? count(devices.offline) : "—"}<small>部</small></strong><small>未有近期上報，請留意現場狀態</small></div></section><section class="panel"><div class="panel-header"><div><h2>所有裝置</h2>${updated}</div><span class="subtle">唯讀檢視</span></div><div class="toolbar"><form id="search-form" class="search-form"><label class="search-box">${icon("search")}<input id="search" aria-label="搜尋裝置名稱或 ID" maxlength="256" placeholder="搜尋裝置名稱或 ID…" value="${e(draft)}"></label><button class="button" type="submit" ${loading ? "disabled" : ""}>搜尋</button></form><label class="filter">連線狀態 <select id="status" ${loading ? "disabled" : ""}><option value="">全部狀態</option><option value="online" ${status === "online" ? "selected" : ""}>在線</option><option value="offline" ${status === "offline" ? "selected" : ""}>離線</option></select></label></div><div class="table-scroll"><table class="devices"><thead><tr><th>裝置</th><th>連線狀態</th><th>最後上報</th><th>Agent 版本</th><th><span class="sr-only">詳情</span></th></tr></thead><tbody>${rows.map((d) => `<tr><td><a class="device-link" href="#/devices/${e(d.deviceId)}"><span class="device-icon">${icon("monitor")}</span><span><strong>${e(d.hostname)}</strong><small>${e(d.deviceId)}</small></span></a></td><td>${badge(d.online)}</td><td><span title="${e(dateTime(d.lastSeenAt))}">${e(relativeTime(d.lastSeenAt))}</span><small class="table-date">${e(dateTime(d.lastSeenAt))}</small></td><td><span class="version">${e(d.agentVersion)}</span></td><td><a class="row-arrow" href="#/devices/${e(d.deviceId)}" aria-label="查看 ${e(d.hostname)} 詳情">${icon("arrow")}</a></td></tr>`).join("")}</tbody></table></div>${!rows.length ? empty(loading ? "正在讀取裝置" : search || status ? "冇符合條件嘅裝置" : "尚未有裝置連接", loading ? "請稍候…" : search || status ? "試吓其他名稱，或者切換連線狀態。" : "Agent 完成註冊後，就會喺呢度顯示。") : ""}<div class="pagination"><span>第 ${history.length + 1} 頁 · 本頁 ${rows.length} 部裝置</span><div><button id="previous" class="button" ${loading || !history.length ? "disabled" : ""}>上一頁</button><button id="next" class="button" ${loading || !devices?.nextCursor ? "disabled" : ""}>下一頁 ${icon("arrow")}</button></div></div></section>`;
  }
  function metric(label, value, note, percentage = null) {
    return `<div class="metric"><span>${e(label)}</span><strong>${e(value)}</strong>${percentage !== null ? `<progress max="100" value="${e(percentage)}" aria-label="${e(label)}"></progress>` : ""}<small>${e(note)}</small></div>`;
  }
  function detailView() {
    const d = detail,
      m = d.metrics,
      h = d.heartbeat,
      i = d.inventory,
      p = d.policyReport;
    const software = [
      ...(i?.software ?? []).map((s) => ({ ...s, type: "應用程式" })),
      ...(i?.runtimes ?? []).map((s) => ({ ...s, type: "執行環境" })),
    ];
    const health = h
      ? ({ online: "運作中", degraded: "需要留意" }[h.status] ?? h.status)
      : "尚未上報";
    return `<div class="detail-meta"><span class="mono">${e(d.deviceId)}</span><span>${e(i?.os ?? "系統待上報")} · ${e(i?.architecture ?? "—")}</span></div><section class="metrics-grid" aria-label="最新效能">${metric("CPU 使用率", m ? `${m.cpuPercent.toFixed(1)}%` : "—", m ? `採樣於 ${dateTime(m.collectedAt)}` : "等待效能上報", m ? m.cpuPercent : null)}${metric("記憶體", m ? bytes(m.memoryUsedBytes) : "—", m ? `總共 ${bytes(m.memoryTotalBytes)}` : "等待效能上報", m ? percent(m.memoryUsedBytes, m.memoryTotalBytes) : null)}${metric("儲存空間", m ? bytes(m.diskUsedBytes) : "—", m ? `總共 ${bytes(m.diskTotalBytes)}` : "等待效能上報", m ? percent(m.diskUsedBytes, m.diskTotalBytes) : null)}${metric("運作時間", uptime(h?.uptimeSeconds), h ? `最後活動 ${relativeTime(h.lastActivity)}` : "等待心跳上報")}</section>${jobsView()}<div class="detail-grid"><section class="panel"><div class="panel-header"><h2>${icon("monitor")}裝置資料</h2></div><dl class="fields">${field("裝置名稱", i?.hostname ?? d.hostname)}${field("作業系統", i?.os)}${field("架構", i?.architecture)}${field("處理器", i?.cpu)}${field("實體記憶體", i ? bytes(i.memoryBytes) : null)}${field("Agent 版本", h?.agentVersion ?? d.agentVersion)}${field("註冊時間", dateTime(d.enrolledAt))}${field("最近盤點", dateTime(i?.collectedAt))}</dl></section><section class="panel"><div class="panel-header"><h2>${icon("pulse")}運作狀態</h2></div><dl class="fields">${field("Agent 狀態", health)}${field("Kiosk 程序", h?.kioskRunning === true ? "運行中" : h?.kioskRunning === false ? "未運行" : "未知")}${field("健康資訊", h?.health)}${field("最後收到上報", dateTime(d.lastSeenAt))}${field("憑證有效至", dateTime(d.credentialExpiresAt))}${field("政策符合情況", p ? ({ compliant: "符合", drifted: "有差異", unknown: "未能判斷" }[p.compliance] ?? p.compliance) : "尚未上報")}</dl>${p?.drift?.length || p?.errors?.length ? `<div class="policy-notes">${[...(p?.drift ?? []), ...(p?.errors ?? [])].map((v) => `<p>${e(v)}</p>`).join("")}</div>` : ""}</section></div><section class="panel section-gap"><div class="panel-header"><h2>網絡介面</h2><span class="subtle">${i?.networks?.length ?? 0} 個介面</span></div>${i?.networks?.length ? `<div class="networks">${i.networks.map((n) => `<div><strong>${e(n.name)}</strong><span class="mono">${e(n.addresses.join(" · ") || "未有位址")}</span></div>`).join("")}</div>` : empty("未有網絡資料", "裝置盤點上報後會喺呢度顯示。")}</section><section class="panel section-gap"><div class="panel-header"><h2>軟件與執行環境</h2><span class="subtle" id="software-count">${software.length} 項</span></div><div class="toolbar"><label class="search-box">${icon("search")}<input id="software-search" aria-label="搜尋軟件" placeholder="搜尋軟件名稱或版本…"></label></div>${software.length ? `<div class="table-scroll software-scroll"><table><thead><tr><th>名稱</th><th>版本</th><th>類別</th></tr></thead><tbody>${software.map((s) => `<tr data-software><td>${e(s.name)}</td><td class="mono">${e(s.version || "—")}</td><td>${e(s.type)}</td></tr>`).join("")}</tbody></table></div>` : empty("未有軟件資料", "等待裝置盤點，或呢部裝置冇回報已安裝軟件。")}</section><section class="panel section-gap"><div class="panel-header"><div><h2>近期上報紀錄</h2><p class="muted">按接收時間由新到舊排列，展開可查看完整內容。</p></div><span class="subtle">第 ${reportHistory.length + 1} 頁</span></div><div class="report-list">${(reports?.items ?? []).map((r) => `<details><summary><span class="report-symbol">${icon(r.path === "/agent/heartbeat" ? "pulse" : "monitor")}</span><span><strong>${e(reportName(r.path))}</strong><small>紀錄 #${e(r.sequence)}</small></span><time>${e(dateTime(r.receivedAt))}</time><span class="expand">＋</span></summary><pre>${e(JSON.stringify(r.body, null, 2))}</pre></details>`).join("")}</div>${!reports?.items?.length ? empty("未有上報紀錄", "Agent 成功同步後，紀錄就會出現。") : ""}<div class="pagination"><span>顯示最近收到嘅資料；採樣時間可能較早。</span><div><button id="newer" class="button" ${loading || !reportHistory.length ? "disabled" : ""}>較新紀錄</button><button id="older" class="button" ${loading || !reports?.nextCursor ? "disabled" : ""}>較舊紀錄</button></div></div></section>`;
  }
  const stateNames = {
    pending: "等待接收", accepted: "已接受", downloading: "模擬下載", verifying: "模擬驗證",
    installing: "模擬安裝", validating: "模擬檢查", rolling_back: "模擬回復",
    succeeded: "完成", failed: "失敗", rolled_back: "已回復", rejected: "Agent 已拒絕",
    expired: "租約已過期", cancelled: "已取消", cancellation_requested: "已要求取消，等待 Agent 確認",
  };
  const canRetry = (state) => ["failed", "rolled_back", "rejected", "expired", "cancelled"].includes(state);
  function jobsView() {
    const enabled = jobs?.simulationEnabled;
    return `<section class="panel section-gap jobs-panel"><div class="panel-header"><div><h2>部署模擬工作</h2><p class="muted">只執行簽署模擬，唔會下載或安裝軟件。Agent 亦需要啟用模擬及信任簽署公鑰。</p></div></div>
      ${mutationNote ? `<p class="job-notice" role="status">${e(mutationNote)}</p>` : ""}
      ${enabled ? `<form id="job-form" class="job-form"><label>目標版本<input id="job-version" required maxlength="256" value="${e(jobVersion)}" placeholder="例如 2.0-test"></label><label>模擬情境<select id="job-scenario">${Object.entries({ success: "成功", download_failure: "下載失敗", hash_failure: "雜湊不符", install_failure: "安裝失敗", validation_failure: "檢查失敗", rollback_failure: "回復失敗", timeout: "逾時" }).map(([key,label]) => `<option value="${key}" ${jobScenario === key ? "selected" : ""}>${label}</option>`).join("")}</select></label><label>初始租約（秒）<input id="job-lease" type="number" min="30" max="3600" required value="${e(jobLease)}"></label><button class="button primary" type="submit" ${loading ? "disabled" : ""}>建立模擬工作</button></form>` : `<p class="job-notice">Server 未啟用模擬簽署，仍可查看現有工作。</p>`}
      <div class="job-list">${(jobs?.items ?? []).map((record) => `<article class="job-card"><div><strong>${e(record.job.targetVersion)}</strong><span class="version">${e(record.scenario)}</span><p>${e(stateNames[record.state] ?? record.state)} · ${record.status ? `${e(record.status.progress)}%` : "—"}</p><small class="mono">${e(record.job.id)}</small><p class="muted">建立於 ${e(dateTime(record.job.createdAt))} · 租約至 ${e(dateTime(record.job.leaseExpiresAt))}</p>${record.retryOf ? `<p class="muted">重試來源：${e(record.retryOf)}</p>` : ""}<p>${e(record.status?.message ?? record.ack?.reason ?? (record.state === "cancelled" ? "已喺 Agent 接收前取消" : record.state === "expired" ? "Agent 未喺租約期限內接收" : "等待 Agent"))}</p></div><div class="job-actions"><button class="button" data-job-history="${e(record.job.id)}" ${loading ? "disabled" : ""}>查看紀錄</button>${canRetry(record.state) && enabled ? `<button class="button" data-job-action="retry" data-job-id="${e(record.job.id)}" ${loading ? "disabled" : ""}>重試模擬</button>` : !["succeeded", "cancellation_requested", "cancelled", "expired", "rejected", "failed", "rolled_back"].includes(record.state) ? `<button class="button" data-job-action="cancel" data-job-id="${e(record.job.id)}" ${loading ? "disabled" : ""}>要求取消</button>` : ""}</div></article>`).join("") || empty("未有模擬工作", "建立工作後，Agent 會喺下次同步時接收。")}</div>
      <div class="pagination"><span>工作第 ${jobHistory.length + 1} 頁 · 按工作 ID 排序</span><div><button id="jobs-previous" class="button" ${loading || !jobHistory.length ? "disabled" : ""}>上一頁</button><button id="jobs-next" class="button" ${loading || !jobs?.nextCursor ? "disabled" : ""}>下一頁</button></div></div>
      ${selectedJob ? `<div class="job-audit"><h3>工作紀錄 · ${e(selectedJob.job.id)}</h3><p class="muted">按 Server 接收時間由新到舊排列。 管理員操作使用共用憑證，未有個人身份。</p><div class="report-list">${selectedJob.events.map((event) => `<details><summary><strong>${e(event.path.split("/").at(-1))}</strong><time>${e(dateTime(event.receivedAt))}</time></summary><pre>${e(JSON.stringify({ actor: event.actor, ...event.payload }, null, 2))}</pre></details>`).join("")}</div><div class="pagination"><span>紀錄第 ${eventHistory.length + 1} 頁 · 本頁 ${selectedJob.events.length} 項</span><div><button id="job-events-newer" class="button" ${loading || !eventHistory.length ? "disabled" : ""}>較新紀錄</button><button id="job-events-older" class="button" ${loading || !selectedJob.nextCursor ? "disabled" : ""}>較舊紀錄</button></div></div></div>` : ""}
    </section>`;
  }
  function bindJobs(view) {
    const form = view.querySelector("#job-form");
    if (form) {
      view.querySelector("#job-version").oninput = (event) => { jobVersion = event.target.value; };
      view.querySelector("#job-scenario").onchange = (event) => { jobScenario = event.target.value; };
      view.querySelector("#job-lease").oninput = (event) => { jobLease = event.target.value; };
      form.onsubmit = (event) => {
        event.preventDefault();
        const body = { deviceId: currentID, targetVersion: jobVersion.trim(), scenario: jobScenario, leaseSeconds: Number(jobLease) };
        if (!body.targetVersion || !Number.isInteger(body.leaseSeconds) || body.leaseSeconds < 30 || body.leaseSeconds > 3600) return;
        mutateJob("create", body);
      };
    }
    for (const button of view.querySelectorAll("[data-job-action]")) button.onclick = () => mutateJob(button.dataset.jobAction, button.dataset.jobId);
    for (const button of view.querySelectorAll("[data-job-history]")) button.onclick = () => { selectedJobID = button.dataset.jobHistory; selectedJob = null; eventCursor = ""; eventHistory = []; load(); };
    const olderEvents = view.querySelector("#job-events-older");
    const newerEvents = view.querySelector("#job-events-newer");
    if (olderEvents) olderEvents.onclick = () => {
      if (!loading && selectedJob?.nextCursor) load({ cursor: selectedJob.nextCursor, history: [...eventHistory, eventCursor] });
    };
    if (newerEvents) newerEvents.onclick = () => {
      if (!loading && eventHistory.length) load({ cursor: eventHistory.at(-1), history: eventHistory.slice(0, -1) });
    };
    view.querySelector("#jobs-next").onclick = () => { if (jobs?.nextCursor) { jobHistory.push(jobCursor); jobCursor = jobs.nextCursor; load(); } };
    view.querySelector("#jobs-previous").onclick = () => { jobCursor = jobHistory.pop() ?? ""; load(); };
  }
  async function mutateJob(action, body) {
    if (!api || loading) return;
    abort();
    const ticket = generation;
    pending = new AbortController();
    const signature = JSON.stringify([action, body]);
    const key = mutationKeys.get(signature) ?? win.crypto.randomUUID();
    mutationKeys.set(signature, key);
    loading = true; error = ""; mutationNote = "正在提交模擬工作操作…"; render();
    try {
      const result = action === "create" ? await api.createJob(body, key, pending.signal) : await api.jobAction(body, action, key, pending.signal);
      if (ticket !== generation) return;
      mutationKeys.delete(signature);
      selectedJobID = result.job.id;
      selectedJob = null; eventCursor = ""; eventHistory = [];
      jobCursor = ""; jobHistory = [];
      mutationNote = action === "cancel" ? "取消要求已記錄。執行中嘅模擬會喺下次續租時停止；已完成嘅步驟仍會保留。" : "模擬工作已建立，等待 Agent 接收。";
      await load();
    } catch (err) {
      if (ticket !== generation) return;
      if (err instanceof APIError && err.status === 401) { logout(err.message); return; }
      if (err instanceof APIError && err.status < 500) mutationKeys.delete(signature);
      loading = false; pending = null; error = message(err);
      mutationNote = "未能確認操作結果。可先重新整理，或用相同內容重試；今次工作階段會沿用同一請求編號。";
      render();
    }
  }
  async function load(eventPage = { cursor: eventCursor, history: eventHistory }) {
    if (!api) return;
    abort();
    const ticket = generation;
    pending = new AbortController();
    const signal = pending.signal;
    loading = true;
    error = "";
    render();
    try {
      if (currentID) {
        const [d, r, j, selected] = await Promise.all([
          api.detail(currentID, signal),
          api.reports(currentID, reportCursor, signal),
          api.jobs(currentID, jobCursor, signal),
          selectedJobID ? api.job(selectedJobID, eventPage.cursor, signal) : Promise.resolve(null),
        ]);
        if (ticket !== generation) return;
        detail = d;
        reports = r;
        jobs = j; selectedJob = selected;
        eventCursor = eventPage.cursor; eventHistory = eventPage.history;
      } else {
        const page = await api.devices({ cursor, search, status, signal });
        if (ticket !== generation) return;
        devices = page;
      }
      lastUpdated = new Date();
    } catch (err) {
      if (ticket !== generation) return;
      if (err instanceof APIError && err.status === 401) {
        logout(err.message);
        return;
      }
      error = message(err);
    } finally {
      if (ticket === generation && api) {
        loading = false;
        pending = null;
        render();
      }
    }
  }
  function route() {
    if (!api) return;
    const match = /^#\/devices\/([^/]+)$/.exec(win.location.hash);
    const id = match?.[1] ?? "";
    if (id && !validID(id)) {
      win.history.replaceState(null, "", "#/devices");
    }
    const nextID = validID(id) ? id : "";
    if (nextID !== currentID) {
      detail = reports = jobs = selectedJob = null;
      jobCursor = selectedJobID = mutationNote = ""; jobHistory = []; eventCursor = ""; eventHistory = [];
      currentID = nextID;
      reportCursor = "";
      reportHistory = [];
      lastUpdated = null;
    }
    load();
  }
  const onHash = () => route();
  win.addEventListener("hashchange", onHash);
  const timer = win.setInterval(() => {
    if (
      api &&
      auto &&
      !loading &&
      !win.document.hidden &&
      !["INPUT", "SELECT"].includes(win.document.activeElement?.tagName) &&
      !root.querySelector("details[open]")
    )
      load();
  }, 20000);
  const onPageHide = () => logout();
  win.addEventListener("pagehide", onPageHide);
  login();
  return {
    destroy() {
      abort();
      api?.clear();
      win.clearInterval(timer);
      win.removeEventListener("hashchange", onHash);
      win.removeEventListener("pagehide", onPageHide);
      root.replaceChildren();
    },
  };
}

if (typeof document !== "undefined" && document.querySelector("#app"))
  mount(document.querySelector("#app"));
