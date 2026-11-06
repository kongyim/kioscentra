import { afterEach, describe, it, expect, vi } from "vitest";
import {
  act,
  cleanup,
  render,
  screen,
  fireEvent,
} from "@testing-library/react";
import App from "./App";
import { api } from "./api";
vi.mock("./api", () => ({
  api: {
    snapshot: vi.fn(),
    uiWarning: vi.fn().mockResolvedValue(""),
    authorize: vi.fn(),
    action: vi.fn(),
  },
}));
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.clearAllMocks();
  window.localStorage.clear();
});
describe("Local console", () => {
  it("shows a disconnected state without fabricated telemetry", async () => {
    vi.mocked(api.snapshot).mockRejectedValue(new Error("Service unavailable"));
    render(<App />);
    expect(await screen.findByText(/Service unavailable/)).toBeTruthy();
    expect(screen.getAllByText("Unavailable").length).toBeGreaterThan(0);
    expect(screen.queryByText("Last sync successful")).toBeNull();
  });
  it("maintenance remains unavailable without a live service", async () => {
    vi.mocked(api.snapshot).mockRejectedValue(new Error("Offline"));
    render(<App />);
    fireEvent.click(screen.getByText("Maintenance"));
    expect(
      (
        screen.getByRole("button", {
          name: "Authorize selected action",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(
      (
        screen.getByRole("button", {
          name: "Refresh inventory",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(api.action).not.toHaveBeenCalled();
  });
  it("persists only the theme preference", () => {
    vi.mocked(api.snapshot).mockReturnValue(new Promise(() => {}));
    render(<App />);
    fireEvent.click(screen.getByRole("button", { name: "Light theme" }));
    expect(window.localStorage.getItem("theme")).toBe("light");
    expect(window.localStorage.length).toBe(1);
  });
});

it("requires scoped authorization and a separate confirmation before stopping", async () => {
  vi.mocked(api.snapshot).mockResolvedValue(connectedSnapshot);
  vi.mocked(api.authorize).mockResolvedValue({
    token: "one-use-grant",
    scope: "stop_service",
    expiresAt: new Date(Date.now() + 120000).toISOString(),
  });
  vi.mocked(api.action).mockResolvedValue(undefined);
  render(<App />);
  await screen.findByText("Service connected");
  fireEvent.click(screen.getByText("Maintenance"));
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "Action" }));
  fireEvent.click(
    await screen.findByRole("option", { name: "Stop device service" }),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Stop device service",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.change(screen.getByLabelText("Technician secret"), {
    target: { value: "test-secret" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Authorize selected action" }),
  );
  await screen.findByText(/Authorized until/);
  expect(api.authorize).toHaveBeenCalledWith("test-secret", "stop_service");
  expect(
    (screen.getByLabelText("Technician secret") as HTMLInputElement).value,
  ).toBe("");
  fireEvent.click(screen.getByRole("button", { name: "Stop device service" }));
  await screen.findByRole("dialog");
  expect(api.action).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Confirm stop" }));
  await screen.findByText("Authorized request sent to the service.");
  expect(api.action).toHaveBeenCalledWith("one-use-grant", "stop_service");
  expect(window.localStorage.length).toBe(0);
  expect(
    (
      (await screen.findByRole("button", {
        name: "Stop device service",
      })) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
});

const connectedSnapshot: Awaited<ReturnType<typeof api.snapshot>> = {
  version: "test",
  commit: "test",
  hostname: "test-device",
  maintenanceEnabled: true,
  logs: [],
  data: {
    queuedEvents: 0,
    "sync-status": null,
    heartbeat: null,
    "monitoring-snapshot": null,
    "inventory-snapshot": null,
    policy: null,
    "policy-observation": null,
    configuration: null,
    jobs: [],
  },
};

it("keeps the console usable when browser storage is blocked", async () => {
  vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
    throw new Error("Storage blocked");
  });
  vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
    throw new Error("Storage blocked");
  });
  vi.mocked(api.snapshot).mockRejectedValue(new Error("Offline"));
  render(<App />);
  await screen.findByText(/Offline/);
  fireEvent.click(screen.getByRole("button", { name: "Light theme" }));
  expect(screen.getByRole("button", { name: "Dark theme" })).toBeTruthy();
});
it("discards an authorization that finishes after service disconnect", async () => {
  vi.mocked(api.snapshot)
    .mockResolvedValueOnce(connectedSnapshot)
    .mockRejectedValue(new Error("Disconnected during authorization"));
  let resolveGrant!: (grant: Awaited<ReturnType<typeof api.authorize>>) => void;
  vi.mocked(api.authorize).mockReturnValue(
    new Promise((resolve) => {
      resolveGrant = resolve;
    }),
  );
  render(<App />);
  await screen.findByText("Service connected");
  fireEvent.click(screen.getByText("Maintenance"));
  fireEvent.change(screen.getByLabelText("Technician secret"), {
    target: { value: "secret" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Authorize selected action" }),
  );
  await screen.findByText(
    /Disconnected during authorization/,
    {},
    { timeout: 5000 },
  );
  await act(async () => {
    resolveGrant({
      token: "late-grant",
      scope: "refresh_inventory",
      expiresAt: new Date(Date.now() + 120000).toISOString(),
    });
  });
  expect(screen.queryByText(/Authorized until/)).toBeNull();
  expect(api.action).not.toHaveBeenCalled();
});

it("shows simulation history without claiming a real installation", async () => {
  vi.mocked(api.snapshot).mockResolvedValue({
    ...connectedSnapshot,
    data: {
      ...connectedSnapshot.data,
      configuration: {
        ServerURL: "https://central.example",
        IntervalSeconds: 30,
        MaxQueue: 1000,
        SimulatedDeployments: true,
      },
      deployments: [
        {
          id: "event",
          jobId: "job",
          delivery: "delivered",
          status: {
            state: "rolled_back",
            progress: 100,
            message:
              "Simulated previous version restored; no software was changed",
            updatedAt: new Date().toISOString(),
          },
        },
      ],
    },
  });
  render(<App />);
  await screen.findByText("Service connected");
  fireEvent.click(screen.getByText("Deployments"));
  expect(
    screen.getByText(/No software is downloaded or installed/),
  ).toBeTruthy();
  expect(screen.getByText("rolled_back")).toBeTruthy();
  expect(screen.getByText(/Report delivered/)).toBeTruthy();
});

it("uses a scoped grant to cancel the active simulation", async () => {
  vi.mocked(api.snapshot).mockResolvedValue(connectedSnapshot);
  vi.mocked(api.authorize).mockResolvedValue({
    token: "cancel-grant",
    scope: "cancel_deployment",
    expiresAt: new Date(Date.now() + 120000).toISOString(),
  });
  vi.mocked(api.action).mockResolvedValue(undefined);
  render(<App />);
  await screen.findByText("Service connected");
  fireEvent.click(screen.getByText("Maintenance"));
  fireEvent.mouseDown(screen.getByRole("combobox", { name: "Action" }));
  fireEvent.click(
    await screen.findByRole("option", { name: "Cancel active simulation" }),
  );
  fireEvent.change(screen.getByLabelText("Technician secret"), {
    target: { value: "test-secret" },
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Authorize selected action" }),
  );
  await screen.findByText(/Authorized until/);
  fireEvent.click(
    screen.getByRole("button", { name: "Cancel active simulation" }),
  );
  await screen.findByText("Authorized request sent to the service.");
  expect(api.action).toHaveBeenCalledWith("cancel-grant", "cancel_deployment");
});

it("shows independent update recovery and pending reports as simulations", async () => {
  vi.mocked(api.snapshot).mockResolvedValue({
    ...connectedSnapshot,
    data: {
      ...connectedSnapshot.data,
      configuration: {
        ServerURL: "https://central.example",
        IntervalSeconds: 30,
        MaxQueue: 1000,
        SimulatedUpdates: true,
      },
      updateTarget: { current: "2.0", knownGood: "1.0" },
      updates: [
        {
          id: "update-event",
          jobId: "update-job",
          delivered: false,
          status: {
            targetVersion: "2.0",
            state: "rolling_back",
            message: "Simulated recovery in progress",
            updatedAt: new Date().toISOString(),
          },
        },
      ],
    },
  });
  render(<App />);
  await screen.findByText("Service connected");
  fireEvent.click(screen.getByText("Updates"));
  expect(
    screen.getByText(/No executable or Windows service is changed/),
  ).toBeTruthy();
  expect(screen.getByText("rolling_back")).toBeTruthy();
  expect(screen.getByText(/Report pending/)).toBeTruthy();
  expect(screen.getByText(/target differs from known-good/)).toBeTruthy();
});
