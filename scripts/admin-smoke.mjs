// Real central API + isolated headless Chrome. No existing browser profile or data is used.
// Build bin/kiosentra-server first. Requires Node >=24 and CHROME_BIN (or macOS Chrome).
import { spawn } from "node:child_process";
import { mkdtemp, readFile, writeFile, mkdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { resolve, join } from "node:path";
import { randomBytes, randomUUID, generateKeyPairSync } from "node:crypto";
import { createServer } from "node:net";
import assert from "node:assert/strict";

const root = resolve(import.meta.dirname, "..");
const output = join(root, ".cache/admin-qa");
await mkdir(output, { recursive: true });
const temporary = await mkdtemp(join(tmpdir(), "kiosentra-admin-"));
const freePort = await new Promise((done, reject) => {
  const s = createServer();
  s.on("error", reject);
  s.listen(0, "127.0.0.1", () => {
    const port = s.address().port;
    s.close(() => done(port));
  });
});
const origin = `http://127.0.0.1:${freePort}`;
const admin = randomBytes(32).toString("hex"),
  enrollment = randomBytes(32).toString("hex");
const env = Object.fromEntries(
  Object.entries(process.env).filter(([key]) => !key.startsWith("KIOSENTRA_")),
);
const signing = generateKeyPairSync("ed25519");
const signingPrivate = Buffer.concat([signing.privateKey.export({ type: "pkcs8", format: "der" }).subarray(-32), signing.publicKey.export({ type: "spki", format: "der" }).subarray(-32)]).toString("base64");
Object.assign(env, {
  KIOSENTRA_CENTRAL_SIMULATION_PRIVATE_KEY: signingPrivate,
  KIOSENTRA_CENTRAL_ADMIN_TOKEN: admin,
  KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN: enrollment,
  KIOSENTRA_CENTRAL_CREDENTIAL_KEY: randomBytes(32).toString("base64"),
});
let server, chrome, ws;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
async function until(check, label) {
  for (let i = 0; i < 100; i++) {
    try {
      const value = await check();
      if (value) return value;
    } catch {}
    await sleep(100);
  }
  throw new Error(`Timeout: ${label}`);
}
async function stop(child) {
  if (!child || child.exitCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([new Promise((r) => child.once("exit", r)), sleep(3000)]);
  if (child.exitCode === null) child.kill("SIGKILL");
}
async function api(path, token, body, method = "POST") {
  const res = await fetch(origin + "/api/v1/" + path, {
    method,
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${token}`,
      "Idempotency-Key": randomUUID(),
    },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  assert.ok(res.ok, `fixture HTTP ${res.status}`);
  return res.status === 204 ? null : res.json();
}
try {
  server = spawn(
    join(root, "bin/kiosentra-server"),
    [
      "--listen",
      `127.0.0.1:${freePort}`,
      "--database",
      join(temporary, "central.db"),
      "--admin-directory",
      join(root, "apps/admin/public"),
    ],
    { env, stdio: "ignore" },
  );
  await until(async () => {
    const r = await fetch(origin + "/admin/");
    return r.ok;
  }, "server startup");
  let primary;
  for (let n = 0; n < 25; n++) {
    const c = await api("enrollments", "", {
      enrollmentToken: enrollment,
      publicKey: randomBytes(32).toString("base64"),
      hostname: `${["HKG-CENTRAL", "HKG-KOWLOON", "HKG-AIRPORT", "HKG-TSUENWAN"][n % 4]}-${String(n + 1).padStart(2, "0")}`,
      agentVersion: "0.1.0-dev",
    });
    if (n === 0) primary = c;
    if (n < 8)
      await api("agent/heartbeat", c.accessToken, {
        agentVersion: "0.1.0-dev",
        status: n === 3 ? "degraded" : "online",
        uptimeSeconds: 353400,
        kioskRunning: n !== 3,
        lastActivity: new Date().toISOString(),
        health: n === 3 ? "kiosk_stopped" : "ok",
      });
  }
  const now = new Date().toISOString();
  await api("agent/metrics", primary.accessToken, {
    collectedAt: now,
    cpuPercent: 24.6,
    memoryTotalBytes: 17179869184,
    memoryUsedBytes: 6012954214,
    diskTotalBytes: 512110190592,
    diskUsedBytes: 182536110080,
    processes: [],
  });
  await api(
    "agent/inventory",
    primary.accessToken,
    {
      collectedAt: now,
      hostname: "HKG-CENTRAL-01",
      os: "Windows 11 Enterprise",
      architecture: "amd64",
      cpu: "Intel Core i5-12500T",
      memoryBytes: 17179869184,
      storageBytes: 512110190592,
      software: [
        { name: "Kiosentra Agent", version: "0.1.0" },
        { name: "Kiosk Application", version: "2.4.1" },
        { name: "Microsoft Edge WebView2", version: "130.0" },
        { name: "<img src=x onerror=alert(1)>", version: "escaped text" },
      ],
      runtimes: [{ name: ".NET Runtime", version: "8.0" }],
      networks: [
        { name: "Ethernet", addresses: ["192.0.2.10", "2001:db8::10"] },
      ],
    },
    "PUT",
  );
  chrome = spawn(
    process.env.CHROME_BIN ??
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    [
      "--headless=new",
      "--disable-gpu",
      "--no-first-run",
      "--no-default-browser-check",
      `--user-data-dir=${join(temporary, "chrome")}`,
      "--remote-debugging-address=127.0.0.1",
      "--remote-debugging-port=0",
      "about:blank",
    ],
    { stdio: "ignore" },
  );
  chrome.on("error", () => {});
  const debugPort = await until(
    async () =>
      (
        await readFile(join(temporary, "chrome/DevToolsActivePort"), "utf8")
      ).split("\n")[0],
    "Chrome startup",
  );
  const target = await (
    await fetch(`http://127.0.0.1:${debugPort}/json/new?about:blank`, {
      method: "PUT",
    })
  ).json();
  ws = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((done, reject) => {
    ws.onopen = done;
    ws.onerror = reject;
  });
  let sequence = 0;
  const requests = new Map();
  const exceptions = [];
  ws.onmessage = (event) => {
    const msg = JSON.parse(event.data);
    if (msg.method === "Runtime.exceptionThrown")
      exceptions.push(msg.params.exceptionDetails.text);
    if (msg.id) {
      const request = requests.get(msg.id);
      requests.delete(msg.id);
      if (msg.error) request.reject(new Error(msg.error.message));
      else request.resolve(msg.result);
    }
  };
  function cdp(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++sequence;
      requests.set(id, { resolve, reject });
      ws.send(JSON.stringify({ id, method, params }));
    });
  }
  async function evaluate(expression) {
    const r = await cdp("Runtime.evaluate", {
      expression,
      returnByValue: true,
      awaitPromise: true,
    });
    if (r.exceptionDetails) throw new Error("Browser evaluation failed");
    return r.result.value;
  }
  async function shot(name) {
    const r = await cdp("Page.captureScreenshot", { format: "png" });
    await writeFile(join(output, name), Buffer.from(r.data, "base64"));
  }
  await cdp("Page.enable");
  await cdp("Runtime.enable");
  await cdp("Emulation.setDeviceMetricsOverride", {
    width: 1440,
    height: 1000,
    deviceScaleFactor: 1,
    mobile: false,
  });
  await cdp("Page.navigate", { url: origin + "/admin/" });
  await until(
    () => evaluate('Boolean(document.querySelector("#token"))'),
    "login form",
  );
  await shot("login.png");
  await evaluate(
    `document.querySelector('#token').value=${JSON.stringify(admin)};document.querySelector('#login-form').requestSubmit();`,
  );
  await until(
    () =>
      evaluate(
        'document.querySelectorAll(".devices tbody tr").length===20 && !document.querySelector("#refresh").disabled',
      ),
    "device list",
  );
  await shot("devices.png");
  assert.equal(
    await evaluate('document.querySelectorAll(".stat strong")[0].textContent'),
    "25",
  );
  await evaluate('document.querySelector("#next").click()');
  await until(
    () =>
      evaluate(
        'document.querySelectorAll(".devices tbody tr").length===5 && !document.querySelector("#refresh").disabled',
      ),
    "second page",
  );
  await evaluate(
    `document.querySelector('#search').value='HKG-CENTRAL-01';document.querySelector('#search').dispatchEvent(new Event('input'));document.querySelector('#search-form').requestSubmit();`,
  );
  await until(
    () =>
      evaluate(
        'document.querySelectorAll(".devices tbody tr").length===1 && !document.querySelector("#refresh").disabled',
      ),
    "server search",
  );
  await evaluate(`location.hash='#/devices/${primary.deviceId}'`);
  await until(
    () =>
      evaluate(
        'Boolean(document.querySelector("#software-search")) && !document.querySelector("#refresh").disabled',
      ),
    "detail",
  );
  assert.equal(await evaluate('document.querySelectorAll("img").length'), 0);
  assert.equal(
    await evaluate(
      'document.querySelector(".report-list summary strong").textContent',
    ),
    "裝置盤點",
  );
  await evaluate(`document.querySelector('#job-version').value='2.0-smoke';document.querySelector('#job-version').dispatchEvent(new Event('input'));document.querySelector('#job-form').dispatchEvent(new Event('submit',{cancelable:true}));`);
  await until(() => evaluate('Boolean(document.querySelector("[data-job-action=cancel]")) && !document.querySelector("#refresh").disabled'), "create signed simulation");
  await evaluate('document.querySelector("[data-job-action=cancel]").click()');
  await until(() => evaluate('Boolean(document.querySelector("[data-job-action=retry]")) && !document.querySelector("#refresh").disabled'), "cancel pending simulation");
  await evaluate('document.querySelector("[data-job-action=retry]").click()');
  await until(() => evaluate('document.querySelectorAll(".job-card").length===2 && !document.querySelector("#refresh").disabled'), "retry simulation");
  const activeJobID = await evaluate('document.querySelector("[data-job-action=cancel]").dataset.jobId');
  await api(`agent/jobs/${activeJobID}/ack`, primary.accessToken, { accepted: true, reason: "simulation" });
  for (let n = 0; n < 103; n++) {
    await api(`agent/jobs/${activeJobID}/renew`, primary.accessToken, { leaseSeconds: 300 });
  }
  const firstHistory = await api(`admin/jobs/${activeJobID}`, admin, undefined, "GET");
  assert.equal(firstHistory.events.length, 100);
  assert.ok(firstHistory.nextCursor);
  const olderHistory = await api(`admin/jobs/${activeJobID}?cursor=${encodeURIComponent(firstHistory.nextCursor)}`, admin, undefined, "GET");
  assert.ok(olderHistory.events.length > 0);
  assert.equal(olderHistory.nextCursor ?? "", "");
  await evaluate('document.querySelector("#refresh").click()');
  await until(() => evaluate('document.querySelectorAll(".job-audit details").length===100 && !document.querySelector("#refresh").disabled'), "first 100 job events");
  assert.equal(await evaluate('document.querySelector("#job-events-newer").disabled'), true);
  await evaluate('document.querySelector("#job-events-older").click()');
  await until(() => evaluate('document.querySelector(".job-audit").textContent.includes("紀錄第 2 頁") && !document.querySelector("#refresh").disabled'), "older job events");
  assert.equal(await evaluate('document.querySelectorAll(".job-audit details").length'), olderHistory.events.length);
  assert.equal(await evaluate('document.querySelector("#job-events-older").disabled'), true);
  assert.ok(await evaluate('document.querySelector(".job-audit").textContent.includes("simulation")'));
  await evaluate('document.querySelector("#refresh").click()');
  await until(() => evaluate('!document.querySelector("#refresh").disabled'), "refresh older history");
  assert.ok(await evaluate('document.querySelector(".job-audit").textContent.includes("紀錄第 2 頁")'));
  await evaluate('document.querySelector(".job-audit").scrollIntoView()');
  await shot("job-history.png");
  await evaluate('document.querySelector("#job-events-newer").click()');
  await until(() => evaluate('document.querySelectorAll(".job-audit details").length===100 && !document.querySelector("#refresh").disabled'), "newer job events");
  await shot("detail.png");
  await evaluate('document.querySelector(".jobs-panel").scrollIntoView()');
  await shot("jobs.png");
  await evaluate("scrollTo(0,0)");
  await evaluate(
    `document.querySelector('#software-search').value='Kiosk';document.querySelector('#software-search').dispatchEvent(new Event('input'));`,
  );
  assert.equal(
    await evaluate(
      'document.querySelectorAll("[data-software]:not([hidden])").length',
    ),
    1,
  );
  await cdp("Emulation.setDeviceMetricsOverride", {
    width: 390,
    height: 844,
    deviceScaleFactor: 1,
    mobile: true,
  });
  assert.ok(
    await evaluate("document.documentElement.scrollWidth<=innerWidth"),
    "mobile page overflows",
  );
  await shot("mobile-detail.png");
  await evaluate('document.querySelector(".jobs-panel").scrollIntoView()');
  await shot("mobile-jobs.png");
  await evaluate('document.querySelector("#job-events-older").click()');
  await until(() => evaluate('document.querySelector(".job-audit").textContent.includes("紀錄第 2 頁") && !document.querySelector("#refresh").disabled'), "mobile older job events");
  await evaluate('document.querySelector(".job-audit").scrollIntoView()');
  assert.ok(await evaluate("document.documentElement.scrollWidth<=innerWidth"), "mobile history overflows");
  await shot("mobile-job-history.png");
  assert.equal(await evaluate("localStorage.length+sessionStorage.length"), 0);
  await evaluate('document.querySelector("#logout").click()');
  await until(
    () => evaluate('Boolean(document.querySelector("#token"))'),
    "logout",
  );
  assert.equal(
    await evaluate('Boolean(document.querySelector(".workspace"))'),
    false,
  );
  assert.deepEqual(exceptions, []);
  console.log(
    "PASS: live API login, pagination, search, details, latest reports, safe text rendering, mobile layout, simulation creation/cancellation/retry, job history pagination/refresh on desktop and mobile, and logout",
  );
  console.log(`Screenshots: ${output}`);
} finally {
  ws?.close();
  await stop(chrome);
  await stop(server);
  await rm(temporary, { recursive: true, force: true });
}
