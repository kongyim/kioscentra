export const escapeHTML = (value) =>
  String(value ?? "").replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ],
  );
export const validID = (value) =>
  /^[a-f\d]{8}(-[a-f\d]{4}){3}-[a-f\d]{12}$/i.test(value ?? "");
export function dateTime(value) {
  const date = new Date(value);
  return value && Number.isFinite(date.getTime())
    ? date.toLocaleString("zh-HK", {
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
        hour: "2-digit",
        minute: "2-digit",
        second: "2-digit",
        hour12: false,
      })
    : "尚未上報";
}
export function relativeTime(value, now = Date.now()) {
  if (!value) return "尚未上報";
  const seconds = Math.floor((now - new Date(value).getTime()) / 1000);
  if (!Number.isFinite(seconds)) return "時間不詳";
  if (seconds < 0) return "時間超前";
  if (seconds < 60) return "剛剛";
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分鐘前`;
  if (seconds < 86400) return `${Math.floor(seconds / 3600)} 小時前`;
  return `${Math.floor(seconds / 86400)} 日前`;
}
export function bytes(value) {
  if (value == null || !Number.isFinite(value) || value < 0) return "—";
  if (value === 0) return "0 B";
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), 4);
  return `${(value / 1024 ** index).toLocaleString("zh-HK", { maximumFractionDigits: 1 })} ${["B", "KiB", "MiB", "GiB", "TiB"][index]}`;
}
export function percent(used, total) {
  return Number.isFinite(used) && total > 0
    ? Math.min(100, Math.max(0, (100 * used) / total))
    : null;
}
export function uptime(seconds) {
  if (!Number.isFinite(seconds)) return "—";
  return seconds >= 86400
    ? `${Math.floor(seconds / 86400)} 日 ${Math.floor((seconds % 86400) / 3600)} 小時`
    : `${Math.floor(seconds / 3600)} 小時 ${Math.floor((seconds % 3600) / 60)} 分鐘`;
}
export const reportName = (path) =>
  ({
    "/agent/heartbeat": "心跳狀態",
    "/agent/metrics": "效能數據",
    "/agent/inventory": "裝置盤點",
    "/agent/policy/report": "政策觀察",
  })[path] ?? "上報紀錄";
export class APIError extends Error {
  constructor(status) {
    super(
      status === 401
        ? "管理員 token 無效或已失效，請重新連接。"
        : status === 409
          ? "工作狀態已改變、裝置有未完成工作，或 Server 未啟用模擬簽署。請重新整理後檢查。"
          : status === 404
          ? "搵唔到呢部裝置。請返回列表重新選擇。"
          : `暫時無法讀取資料（${status}），請稍後重試。`,
    );
    this.status = status;
  }
}
export class AdminAPI {
  #token;
  constructor(token, fetcher = globalThis.fetch.bind(globalThis)) {
    this.#token = token;
    this.fetcher = fetcher;
  }
  clear() {
    this.#token = "";
  }
  async request(path, { signal, method = "GET", body, key } = {}) {
    if (!this.#token) throw new APIError(401);
    const response = await this.fetcher(`/api/v1/admin/${path}`, {
      method,
      headers: { Authorization: `Bearer ${this.#token}`, ...(body === undefined ? {} : { "Content-Type": "application/json", "Idempotency-Key": key }) },
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: "omit",
      cache: "no-store",
      redirect: "error",
      signal: AbortSignal.any([
        ...(signal ? [signal] : []),
        AbortSignal.timeout(12000),
      ]),
    });
    if (!response.ok) throw new APIError(response.status);
    return response.status === 204 ? null : response.json();
  }
  get(path, signal) { return this.request(path, { signal }); }
  jobs(deviceId, cursor = "", signal) {
    if (!validID(deviceId)) throw new APIError(404);
    return this.get(`jobs?${new URLSearchParams({ deviceId, cursor, limit: "20" })}`, signal);
  }
  job(id, cursor = "", signal) {
    if (!validID(id)) throw new APIError(404);
    return this.get(`jobs/${id}?${new URLSearchParams({ limit: "100", cursor })}`, signal);
  }
  createJob(body, key, signal) { return this.request("jobs", { method: "POST", body, key, signal }); }
  jobAction(id, action, key, signal) {
    if (!validID(id) || !["cancel", "retry"].includes(action)) throw new APIError(404);
    return this.request(`jobs/${id}/${action}`, { method: "POST", body: {}, key, signal });
  }
  devices({ cursor = "", search = "", status = "", signal } = {}) {
    return this.get(
      `devices?${new URLSearchParams({ limit: "20", cursor, search, status })}`,
      signal,
    );
  }
  detail(id, signal) {
    if (!validID(id)) throw new APIError(404);
    return this.get(`devices/${id}`, signal);
  }
  reports(id, cursor = "", signal) {
    if (!validID(id)) throw new APIError(404);
    return this.get(
      `devices/${id}/reports?${new URLSearchParams({ limit: "20", order: "desc", cursor })}`,
      signal,
    );
  }
}
