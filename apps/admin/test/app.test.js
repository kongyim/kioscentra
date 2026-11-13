import test from "node:test";
import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { mount } from "../public/app.js";
import { APIError } from "../public/core.js";
const id = "00000000-0000-0000-0000-000000000001";
const device = {
  deviceId: id,
  hostname: "<img src=x onerror=alert(1)>",
  online: true,
  agentVersion: "1.0",
  lastSeenAt: new Date().toISOString(),
};
const page = {
  items: [device],
  nextCursor: "next",
  total: 21,
  online: 1,
  offline: 20,
};
const tick = () => new Promise((resolve) => setImmediate(resolve));
function setup(t, api) {
  api.jobs ??= async () => ({ items: [], nextCursor: "", simulationEnabled: false });
  const dom = new JSDOM('<div id="root"></div>', {
    url: "https://central.test/admin/",
  });
  const root = dom.window.document.querySelector("#root");
  const app = mount(root, { window: dom.window, apiFactory: () => api });
  t.after(() => {
    app.destroy();
    dom.window.close();
  });
  return {
    dom,
    root,
    async login() {
      root.querySelector("#token").value = "secret";
      root
        .querySelector("form")
        .dispatchEvent(
          new dom.window.Event("submit", { bubbles: true, cancelable: true }),
        );
      await tick();
      await tick();
    },
  };
}
test("login, server pagination/filter, safe detail, unknown telemetry and logout", async (t) => {
  const calls = [];
  let cleared = false;
  const api = {
    devices: async (options) => {
      calls.push(options);
      return page;
    },
    detail: async () => ({
      ...device,
      heartbeat: null,
      metrics: null,
      inventory: {
        software: [{ name: "<script>unsafe</script>", version: "1" }],
        runtimes: [],
        networks: [],
      },
      policyReport: null,
    }),
    reports: async () => ({ items: [], nextCursor: "" }),
    clear() {
      cleared = true;
    },
  };
  const { root, dom, login } = setup(t, api);
  await login();
  assert.equal(root.querySelectorAll("img").length, 0);
  assert.ok(root.textContent.includes(device.hostname));
  root.querySelector("#next").click();
  await tick();
  assert.equal(calls.at(-1).cursor, "next");
  root.querySelector("#search").value = "north";
  root.querySelector("#search").dispatchEvent(new dom.window.Event("input"));
  root
    .querySelector("#search-form")
    .dispatchEvent(new dom.window.Event("submit", { cancelable: true }));
  await tick();
  assert.equal(calls.at(-1).search, "north");
  assert.equal(calls.at(-1).cursor, "");
  root.querySelector("#status").value = "offline";
  root.querySelector("#status").dispatchEvent(new dom.window.Event("change"));
  await tick();
  assert.equal(calls.at(-1).status, "offline");
  dom.window.location.hash = `#/devices/${id}`;
  dom.window.dispatchEvent(new dom.window.Event("hashchange"));
  await tick();
  assert.ok(root.textContent.includes("未知"));
  assert.ok(root.textContent.includes("<script>unsafe</script>"));
  assert.equal(root.querySelectorAll("script").length, 0);
  assert.equal(dom.window.localStorage.length, 0);
  assert.equal(dom.window.sessionStorage.length, 0);
  root.querySelector("#logout").click();
  assert.ok(cleared);
  assert.ok(root.querySelector("#token"));
  assert.ok(!root.textContent.includes(device.hostname));
});
test("401 clears session and an older response cannot repopulate after logout", async (t) => {
  let fail = false,
    resolve;
  let cleared = 0;
  const api = {
    devices: async () => {
      if (fail) throw new APIError(401);
      return page;
    },
    detail: () =>
      new Promise((r) => {
        resolve = r;
      }),
    reports: async () => ({ items: [] }),
    clear() {
      cleared++;
    },
  };
  const { root, dom, login } = setup(t, api);
  await login();
  fail = true;
  root.querySelector("#refresh").click();
  await tick();
  assert.ok(root.querySelector("#token"));
  assert.ok(root.textContent.includes("無效"));
  assert.equal(cleared, 1);
  fail = false;
  await login();
  dom.window.location.hash = `#/devices/${id}`;
  dom.window.dispatchEvent(new dom.window.Event("hashchange"));
  await tick();
  root.querySelector("#logout").click();
  resolve({ ...device });
  await tick();
  assert.ok(root.querySelector("#token"));
  assert.ok(!root.querySelector(".workspace"));
});
test("network error preserves data with a visible stale-data notice and retry", async (t) => {
  let fail = false;
  const api = {
    devices: async () => {
      if (fail) throw new Error("network");
      return page;
    },
    clear() {},
  };
  const { root, login } = setup(t, api);
  await login();
  fail = true;
  root.querySelector("#refresh").click();
  await tick();
  assert.ok(
    root.querySelector('[role="alert"]').textContent.includes("上次成功"),
  );
  assert.ok(root.querySelector(".device-link"));
  fail = false;
  root.querySelector("#refresh").click();
  await tick();
  assert.equal(root.querySelector(".error-banner"), null);
});

test("simulation creation retries a lost response with the same key, then cancels and retries", async (t) => {
  const jobID = "00000000-0000-0000-0000-000000000002";
  const record = { job: { id: jobID, targetVersion: "<script>2.0</script>", createdAt: new Date().toISOString(), leaseExpiresAt: new Date().toISOString() }, scenario: "success", state: "pending", events: [] };
  let created = false;
  const keys = [], actions = [];
  const api = {
    devices: async () => page, detail: async () => device,
    reports: async () => ({ items: [] }),
    jobs: async () => ({ items: created ? [record] : [], simulationEnabled: true }),
    job: async () => record,
    createJob: async (body, key) => {
      keys.push(key);
      assert.equal(body.deviceId, id);
      if (keys.length === 1) throw new Error("response lost");
      created = true;
      return record;
    },
    jobAction: async (id, action) => { actions.push(action); record.state = action === "cancel" ? "cancelled" : "pending"; return record; },
    clear() {},
  };
  const { root, dom, login } = setup(t, api);
  await login();
  dom.window.location.hash = `#/devices/${id}`;
  dom.window.dispatchEvent(new dom.window.Event("hashchange"));
  await tick(); await tick();
  const input = root.querySelector("#job-version");
  input.value = "2.0"; input.dispatchEvent(new dom.window.Event("input"));
  const submit = () => root.querySelector("#job-form").dispatchEvent(new dom.window.Event("submit", { cancelable: true }));
  submit(); await tick();
  assert.match(root.textContent, /未能確認操作結果/);
  submit(); await tick(); await tick();
  assert.equal(keys.length, 2); assert.equal(keys[0], keys[1]);
  assert.equal(root.querySelectorAll("script").length, 0);
  assert.ok(root.textContent.includes(record.job.targetVersion));
  root.querySelector('[data-job-action="cancel"]').click(); await tick(); await tick();
  assert.ok(root.querySelector('[data-job-action="retry"]'));
  root.querySelector('[data-job-action="retry"]').click(); await tick(); await tick();
  assert.deepEqual(actions, ["cancel", "retry"]);
  assert.equal(dom.window.localStorage.length + dom.window.sessionStorage.length, 0);
});

test("late mutation response cannot restore a logged-out session", async (t) => {
  let resolve;
  const api = {
    devices: async () => page, detail: async () => device, reports: async () => ({ items: [] }),
    jobs: async () => ({ items: [], simulationEnabled: true }),
    createJob: () => new Promise((done) => { resolve = done; }), clear() {},
  };
  const { root, dom, login } = setup(t, api);
  await login(); dom.window.location.hash = `#/devices/${id}`;
  dom.window.dispatchEvent(new dom.window.Event("hashchange")); await tick(); await tick();
  const input = root.querySelector("#job-version"); input.value = "2.0"; input.dispatchEvent(new dom.window.Event("input"));
  root.querySelector("#job-form").dispatchEvent(new dom.window.Event("submit", { cancelable: true }));
  root.querySelector("#logout").click();
  resolve({ job: { id } }); await tick();
  assert.ok(root.querySelector("#token")); assert.equal(root.querySelector(".workspace"), null);
});

test("job history pages commit only on success, refresh in place, and reset on selection", async (t) => {
  const jobID = "00000000-0000-0000-0000-000000000002";
  const otherID = "00000000-0000-0000-0000-000000000003";
  const record = (id) => ({ job: { id, targetVersion: "2.0" }, state: "succeeded", scenario: "success" });
  const calls = [];
  let fail = false, delayed = false, resolve;
  const api = {
    devices: async () => page, detail: async () => device, reports: async () => ({ items: [] }),
    jobs: async () => ({ items: [record(jobID), record(otherID)] }),
    job: async (id, cursor) => {
      calls.push({ id, cursor });
      if (delayed) return new Promise((done) => { resolve = done; });
      if (fail) throw new Error("offline");
      return { ...record(id), nextCursor: cursor ? "" : "100", events: [{ path: "/status", payload: { message: cursor ? "older-event" : "newest-event" } }] };
    },
    clear() {},
  };
  const { root, dom, login } = setup(t, api);
  await login();
  dom.window.location.hash = `#/devices/${id}`;
  dom.window.dispatchEvent(new dom.window.Event("hashchange"));
  await tick(); await tick();
  root.querySelector(`[data-job-history="${jobID}"]`).click(); await tick();
  assert.equal(root.querySelector("#job-events-newer").disabled, true);
  fail = true;
  root.querySelector("#job-events-older").click(); await tick();
  assert.match(root.querySelector(".job-audit").textContent, /紀錄第 1 頁/);
  assert.match(root.querySelector(".job-audit").textContent, /newest-event/);
  fail = false;
  root.querySelector("#job-events-older").click(); await tick();
  assert.equal(calls.at(-1).cursor, "100");
  assert.match(root.querySelector(".job-audit").textContent, /紀錄第 2 頁/);
  assert.match(root.querySelector(".job-audit").textContent, /older-event/);
  assert.equal(root.querySelector("#job-events-older").disabled, true);
  root.querySelector("#refresh").click(); await tick();
  assert.equal(calls.at(-1).cursor, "100");
  root.querySelector("#job-events-newer").click(); await tick();
  assert.equal(calls.at(-1).cursor, "");
  root.querySelector("#job-events-older").click(); await tick();
  root.querySelector(`[data-job-history="${otherID}"]`).click(); await tick();
  assert.deepEqual(calls.at(-1), { id: otherID, cursor: "" });
  assert.equal(root.querySelector("#job-events-newer").disabled, true);
  delayed = true;
  root.querySelector("#job-events-older").click();
  assert.equal(root.querySelector("#job-events-older").disabled, true);
  root.querySelector("#logout").click();
  resolve({ ...record(otherID), events: [] }); await tick();
  assert.ok(root.querySelector("#token"));
  assert.equal(root.querySelector(".job-audit"), null);
});
