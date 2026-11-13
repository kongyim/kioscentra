import test from "node:test";
import assert from "node:assert/strict";
import {
  AdminAPI,
  APIError,
  escapeHTML,
  bytes,
  percent,
  relativeTime,
} from "../public/core.js";

test("API stays on same origin, scopes token to header, and uses descending history", async () => {
  const calls = [];
  const api = new AdminAPI("private-token", async (url, options) => {
    calls.push({ url, options });
    return { ok: true, json: async () => ({ items: [], nextCursor: "" }) };
  });
  await api.devices({
    search: "north & south",
    status: "offline",
    cursor: "cursor",
  });
  await api.reports("00000000-0000-0000-0000-000000000001", "42");
  assert.match(calls[0].url, /^\/api\/v1\/admin\/devices\?/);
  assert.equal(
    new URL(calls[0].url, "https://local").searchParams.get("search"),
    "north & south",
  );
  assert.equal(calls[0].options.headers.Authorization, "Bearer private-token");
  assert.equal(calls[0].options.redirect, "error");
  assert.equal(calls[0].options.cache, "no-store");
  assert.match(calls[1].url, /order=desc/);
  assert.ok(!calls[0].url.includes("private-token"));
  api.clear();
  await assert.rejects(api.devices(), (error) => error.status === 401);
});
test("authorization errors are explicit and formatting preserves unknowns", async () => {
  const api = new AdminAPI("bad", async () => ({ ok: false, status: 401 }));
  await assert.rejects(api.devices(), APIError);
  assert.throws(() => api.detail("../secrets"), APIError);
  assert.equal(bytes(null), "—");
  assert.equal(bytes(0), "0 B");
  assert.equal(percent(0, 0), null);
  assert.equal(relativeTime(null), "尚未上報");
  assert.equal(
    escapeHTML('<img src=x onerror="alert(1)">'),
    "&lt;img src=x onerror=&quot;alert(1)&quot;&gt;",
  );
});

test("job mutations use JSON and caller-stable idempotency keys", async () => {
  const calls = [];
  const api = new AdminAPI("secret", async (url, options) => {
    calls.push({ url, options });
    return { ok: true, status: 200, json: async () => ({}) };
  });
  const id = "00000000-0000-0000-0000-000000000001";
  await api.createJob({ deviceId: id, targetVersion: "2.0", scenario: "success", leaseSeconds: 300 }, "stable-key");
  await api.jobAction(id, "cancel", "cancel-key");
  assert.equal(calls[0].options.method, "POST");
  assert.equal(calls[0].options.headers["Idempotency-Key"], "stable-key");
  assert.equal(calls[0].options.headers["Content-Type"], "application/json");
  assert.equal(JSON.parse(calls[0].options.body).deviceId, id);
  assert.equal(calls[1].url, `/api/v1/admin/jobs/${id}/cancel`);
  assert.throws(() => api.jobAction(id, "execute", "key"), APIError);
});

test("job history sends an encoded cursor, page limit and abort signal", async () => {
  let request;
  const api = new AdminAPI("secret", async (url, options) => {
    request = { url, options };
    return { ok: true, json: async () => ({ events: [], nextCursor: "" }) };
  });
  const controller = new AbortController();
  const id = "00000000-0000-0000-0000-000000000001";
  await api.job(id, "100&other=value", controller.signal);
  const url = new URL(request.url, "https://central.test");
  assert.equal(url.pathname, `/api/v1/admin/jobs/${id}`);
  assert.equal(url.searchParams.get("limit"), "100");
  assert.equal(url.searchParams.get("cursor"), "100&other=value");
  assert.equal(url.searchParams.has("other"), false);
  controller.abort();
  assert.equal(request.options.signal.aborted, true);
  assert.throws(() => api.job("../secret"), APIError);
});
