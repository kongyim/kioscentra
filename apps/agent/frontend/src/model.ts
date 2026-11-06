export type Diagnostic = { source: string; durationMs: number; error: string };
export type Software = { name: string; version: string };
export type Policy = {
  revision: string;
  kioskExecutable: string;
  expectedShell: string;
  startupEntries: string[];
  arguments: string[];
  autoLaunch: boolean;
  restartPolicy: string;
  monitorIntervalSeconds: number;
};
export type Snapshot = {
  version: string;
  commit: string;
  hostname: string;
  maintenanceEnabled: boolean;
  uiWarning?: string;
  logs: { time: string; level: string; message: string }[];
  data: {
    queuedEvents: number;
    "sync-status": {
      observedAt: string;
      centralReachable: boolean;
      consecutiveFailures: number;
    } | null;
    heartbeat: {
      status: string;
      uptimeSeconds: number;
      kioskRunning: boolean | null;
      lastActivity: string;
      health: string;
    } | null;
    "monitoring-snapshot": {
      Metrics: {
        collectedAt: string;
        cpuPercent: number;
        memoryTotalBytes: number;
        memoryUsedBytes: number;
        diskTotalBytes: number;
        diskUsedBytes: number;
        processes: { pid: number; cpuPercent: number; memoryBytes: number }[];
      };
      Diagnostics: Diagnostic[];
    } | null;
    "inventory-snapshot": {
      Inventory: {
        collectedAt: string;
        hostname: string;
        os: string;
        architecture: string;
        cpu: string;
        memoryBytes: number;
        storageBytes: number;
        software: Software[];
        runtimes: Software[];
        networks: { name: string; addresses: string[] }[];
      };
      Diagnostics: Diagnostic[];
    } | null;
    policy: Policy | null;
    "policy-observation": {
      revision: string;
      observedAt: string;
      compliance: string;
      drift: string[];
      errors: string[];
    } | null;
    configuration: {
      ServerURL: string;
      IntervalSeconds: number;
      MaxQueue: number;
      SimulatedDeployments?: boolean;
      SimulatedUpdates?: boolean;
    } | null;
    updateTarget?: { current: string; knownGood: string };
    updates?: {
      id: string;
      jobId: string;
      status: {
        targetVersion: string;
        state: string;
        message: string;
        updatedAt: string;
      };
      delivered: boolean;
    }[];
    deployments?: {
      id: string;
      jobId: string;
      status: {
        state: string;
        progress: number;
        message: string;
        updatedAt: string;
      };
      delivery: string;
    }[];
    jobs: {
      id: string;
      kind: string;
      targetVersion: string;
      accepted: boolean;
      reason: string;
      reported: boolean;
    }[];
  };
};
export type Grant = { token: string; scope: string; expiresAt: string };
export const pages = [
  "Overview",
  "Device",
  "Applications",
  "Deployments",
  "Updates",
  "Configuration",
  "Logs",
  "Maintenance",
  "About",
] as const;
export type Page = (typeof pages)[number];
export function bytes(v: number | undefined) {
  if (v === undefined || v <= 0) return "Unavailable";
  return `${(v / 1024 ** 3).toFixed(1)} GB`;
}
export function timestamp(v?: string) {
  return v ? new Date(v).toLocaleString() : "Not collected";
}
export function available(ds: Diagnostic[] | undefined, source: string) {
  return !!ds && ds.some((d) => d.source === source && !d.error);
}
