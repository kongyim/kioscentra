import { useEffect, useMemo, useRef, useState } from "react";
import {
  Alert,
  AppBar,
  Box,
  Button,
  Chip,
  CircularProgress,
  CssBaseline,
  Dialog,
  DialogActions,
  DialogContent,
  DialogTitle,
  Divider,
  FormControl,
  InputLabel,
  List,
  ListItemButton,
  ListItemText,
  MenuItem,
  Paper,
  Select,
  Stack,
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableRow,
  TextField,
  ThemeProvider,
  Toolbar,
  Typography,
  createTheme,
} from "@mui/material";
import { api } from "./api";
import {
  available,
  bytes,
  pages,
  timestamp,
  type Grant,
  type Page,
  type Snapshot,
} from "./model";
const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e));
function readTheme(): "dark" | "light" {
  try {
    return window.localStorage.getItem("theme") === "light" ? "light" : "dark";
  } catch {
    return "dark";
  }
}
function Panel({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <Paper variant="outlined" sx={{ p: 3, borderRadius: 3 }}>
      <Typography variant="h6" mb={2}>
        {title}
      </Typography>
      {children}
    </Paper>
  );
}
function Fields({ rows }: { rows: [string, React.ReactNode][] }) {
  return (
    <Table size="small">
      <TableBody>
        {rows.map(([label, value], index) => (
          <TableRow key={`${label}-${index}`}>
            <TableCell
              sx={{
                color: "text.secondary",
                width: "35%",
                verticalAlign: "top",
              }}
            >
              {label}
            </TableCell>
            <TableCell sx={{ overflowWrap: "anywhere" }}>
              {value || "Unavailable"}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
function Empty({ children }: { children: React.ReactNode }) {
  return (
    <Typography color="text.secondary" py={2}>
      {children}
    </Typography>
  );
}
export default function App() {
  const [page, setPage] = useState<Page>("Overview");
  const [mode, setMode] = useState<"dark" | "light">(() => readTheme());
  const theme = useMemo(
    () =>
      createTheme({
        palette: {
          mode,
          primary: { main: mode === "dark" ? "#48d9bb" : "#087e69" },
          background: {
            default: mode === "dark" ? "#10151d" : "#f2f5f7",
            paper: mode === "dark" ? "#171e29" : "#fff",
          },
        },
        typography: {
          fontFamily: "Inter, Segoe UI, system-ui, sans-serif",
          h4: { fontWeight: 650, letterSpacing: "-.04em" },
          h6: { fontSize: 17, fontWeight: 600 },
        },
        shape: { borderRadius: 10 },
        components: {
          MuiButton: {
            styleOverrides: {
              root: { textTransform: "none", fontWeight: 600 },
            },
          },
        },
      }),
    [mode],
  );
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null),
    [connected, setConnected] = useState(false),
    [connectionError, setConnectionError] = useState(
      "Connecting to the device service…",
    );
  const [query, setQuery] = useState("");
  const [uiWarning, setUIWarning] = useState("");
  useEffect(() => {
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const warning = await api.uiWarning();
        if (!stopped) setUIWarning(warning);
      } catch {
        /* Native bridge may not be available in browser preview. */
      }
      try {
        const next = await api.snapshot();
        if (!stopped) {
          setSnapshot(next);
          setConnected(true);
          setConnectionError("");
        }
      } catch (e) {
        if (!stopped) {
          setConnected(false);
          setConnectionError(errorText(e));
        }
      } finally {
        if (!stopped) timer = setTimeout(poll, 3000);
      }
    };
    void poll();
    return () => {
      stopped = true;
      clearTimeout(timer);
    };
  }, []);
  const data = snapshot?.data,
    metrics = data?.["monitoring-snapshot"],
    inventory = data?.["inventory-snapshot"],
    heartbeat = data?.heartbeat,
    policy = data?.policy,
    report = data?.["policy-observation"];
  const software = (inventory?.Inventory.software ?? []).filter((x) =>
    `${x.name} ${x.version}`.toLowerCase().includes(query.toLowerCase()),
  );
  const diagnostics = [
    ...(metrics?.Diagnostics ?? []),
    ...(inventory?.Diagnostics ?? []),
  ].filter((d) => d.error);
  const metricCards: [string, string][] = [
    [
      "CPU",
      metrics && available(metrics.Diagnostics, "cpu")
        ? `${metrics.Metrics.cpuPercent.toFixed(1)}%`
        : "Unavailable",
    ],
    [
      "Memory",
      metrics && available(metrics.Diagnostics, "memory")
        ? `${bytes(metrics.Metrics.memoryUsedBytes)} / ${bytes(metrics.Metrics.memoryTotalBytes)}`
        : "Unavailable",
    ],
    [
      "Disk used",
      metrics && available(metrics.Diagnostics, "disk")
        ? `${bytes(metrics.Metrics.diskUsedBytes)} / ${bytes(metrics.Metrics.diskTotalBytes)}`
        : "Unavailable",
    ],
    [
      "Kiosk",
      heartbeat?.kioskRunning == null
        ? "Unknown"
        : heartbeat.kioskRunning
          ? "Running"
          : "Stopped",
    ],
  ];
  return (
    <ThemeProvider theme={theme}>
      <CssBaseline />
      <Box sx={{ display: "flex", minHeight: "100vh" }}>
        <Box
          component="nav"
          aria-label="Console pages"
          sx={{
            width: 220,
            flexShrink: 0,
            borderRight: 1,
            borderColor: "divider",
            p: 2,
            position: "fixed",
            height: "100vh",
            bgcolor: "background.paper",
          }}
        >
          <Stack direction="row" gap={1.5} alignItems="center" p={1} mb={4}>
            <Box
              sx={{
                bgcolor: "primary.main",
                color: "#10151d",
                fontSize: 24,
                fontWeight: 800,
                width: 38,
                height: 38,
                borderRadius: 2,
                textAlign: "center",
              }}
            >
              K
            </Box>
            <Box>
              <Typography fontWeight={700}>Kiosentra</Typography>
              <Typography variant="caption" color="text.secondary">
                DEVICE CONSOLE
              </Typography>
            </Box>
          </Stack>
          <Typography variant="overline" color="text.secondary" px={2}>
            Workspace
          </Typography>
          <List>
            {pages.map((name, i) => (
              <ListItemButton
                key={name}
                selected={page === name}
                onClick={() => setPage(name)}
                sx={{ borderRadius: 2, mb: 0.5 }}
              >
                <Typography
                  aria-hidden
                  sx={{ width: 28, color: "text.secondary", fontSize: 12 }}
                >
                  {String(i + 1).padStart(2, "0")}
                </Typography>
                <ListItemText primary={name} />
              </ListItemButton>
            ))}
          </List>
          <Box sx={{ position: "absolute", bottom: 22, left: 24, right: 24 }}>
            <Chip
              size="small"
              label={connected ? "Service connected" : "Reconnecting"}
              color={connected ? "success" : "default"}
              variant="outlined"
            />
            <Typography
              variant="caption"
              display="block"
              mt={1}
              color="text.secondary"
            >
              Local session · View access
            </Typography>
          </Box>
        </Box>
        <Box component="main" sx={{ ml: "220px", flex: 1, minWidth: 0 }}>
          <AppBar
            position="static"
            color="transparent"
            elevation={0}
            sx={{ borderBottom: 1, borderColor: "divider" }}
          >
            <Toolbar>
              <Typography
                variant="body2"
                color="text.secondary"
                sx={{ flex: 1 }}
              >
                {snapshot?.hostname ?? "Device service"} / {page}
              </Typography>
              <Button
                size="small"
                onClick={() => {
                  const next = mode === "dark" ? "light" : "dark";
                  setMode(next);
                  try {
                    window.localStorage.setItem("theme", next);
                  } catch {
                    // The console remains usable when storage is disabled.
                  }
                }}
              >
                {mode === "dark" ? "Light theme" : "Dark theme"}
              </Button>
            </Toolbar>
          </AppBar>
          <Stack
            spacing={3}
            sx={{ p: { xs: 2, md: 4 }, maxWidth: 1400, mx: "auto" }}
          >
            <Box>
              <Typography variant="overline" color="primary.main">
                LOCAL OPERATIONS
              </Typography>
              <Typography variant="h4">
                {page === "Overview" ? "Device overview" : page}
              </Typography>
              <Typography color="text.secondary" mt={1}>
                {page === "Overview"
                  ? "A live view of this device, its applications and service health."
                  : "Service-owned information for this device."}
              </Typography>
            </Box>
            {!connected && (
              <Alert severity="warning">
                {connectionError}
                {snapshot
                  ? " Showing the last received snapshot; values may be stale."
                  : " No service data is available yet."}
              </Alert>
            )}
            {uiWarning && (
              <Alert severity="warning">
                Console shortcut or native window issue: {uiWarning}
              </Alert>
            )}
            {page === "Overview" && (
              <>
                <Box
                  sx={{
                    display: "grid",
                    gridTemplateColumns: "repeat(2,minmax(0,1fr))",
                    gap: 2,
                  }}
                >
                  {metricCards.map(([label, value]) => (
                    <Paper
                      key={label}
                      variant="outlined"
                      sx={{ p: 3, borderRadius: 3 }}
                    >
                      <Typography color="text.secondary" variant="body2">
                        {label}
                      </Typography>
                      <Typography variant="h5" fontWeight={600} mt={1.5}>
                        {value}
                      </Typography>
                    </Paper>
                  ))}
                </Box>
                <Panel title="Service health">
                  <Fields
                    rows={[
                      [
                        "Central server",
                        data?.["sync-status"]
                          ? data["sync-status"].centralReachable
                            ? "Last sync successful"
                            : "Offline / retrying"
                          : "Not observed",
                      ],
                      ["Health", heartbeat?.health?.replaceAll("_", " ")],
                      ["Collected", timestamp(heartbeat?.lastActivity)],
                      [
                        "Queued reports",
                        data ? String(data.queuedEvents) : "Unavailable",
                      ],
                      [
                        "Uptime",
                        heartbeat && available(metrics?.Diagnostics, "uptime")
                          ? `${Math.floor(heartbeat.uptimeSeconds / 3600)}h ${Math.floor(heartbeat.uptimeSeconds / 60) % 60}m`
                          : "Unavailable",
                      ],
                    ]}
                  />
                </Panel>
                <Panel title="Collection diagnostics">
                  {diagnostics.length ? (
                    diagnostics.map((d, i) => (
                      <Alert key={i} severity="warning" sx={{ mb: 1 }}>
                        {d.source}: {d.error}
                      </Alert>
                    ))
                  ) : (
                    <Empty>
                      {snapshot
                        ? "No source errors in the current snapshot."
                        : "Waiting for the first service snapshot."}
                    </Empty>
                  )}
                </Panel>
              </>
            )}
            {page === "Device" && (
              <>
                <Panel title="Hardware & operating system">
                  <Fields
                    rows={[
                      ["Hostname", inventory?.Inventory.hostname],
                      ["Operating system", inventory?.Inventory.os],
                      ["Architecture", inventory?.Inventory.architecture],
                      ["Processor", inventory?.Inventory.cpu],
                      [
                        "Installed memory",
                        bytes(inventory?.Inventory.memoryBytes),
                      ],
                      [
                        "State-volume capacity",
                        bytes(inventory?.Inventory.storageBytes),
                      ],
                      [
                        "Inventory collected",
                        timestamp(inventory?.Inventory.collectedAt),
                      ],
                    ]}
                  />
                </Panel>
                <Panel title="Network interfaces">
                  {inventory?.Inventory.networks.length ? (
                    inventory.Inventory.networks.map((n) => (
                      <Box key={n.name} mb={2}>
                        <Typography fontWeight={600}>{n.name}</Typography>
                        <Typography color="text.secondary">
                          {n.addresses.join(" · ") || "No address reported"}
                        </Typography>
                      </Box>
                    ))
                  ) : (
                    <Empty>No network inventory available.</Empty>
                  )}
                </Panel>
              </>
            )}
            {page === "Applications" && (
              <>
                <Alert severity="info">
                  Inventory is read-only and may be partial. Installing or
                  removing software is not available.
                </Alert>
                <Panel
                  title={`Installed applications${inventory ? ` · ${inventory.Inventory.software.length}` : ""}`}
                >
                  <TextField
                    size="small"
                    label="Search applications"
                    value={query}
                    onChange={(e) => setQuery(e.target.value)}
                    fullWidth
                    sx={{ mb: 2 }}
                  />
                  <Table size="small">
                    <TableHead>
                      <TableRow>
                        <TableCell>Application</TableCell>
                        <TableCell>Version</TableCell>
                      </TableRow>
                    </TableHead>
                    <TableBody>
                      {software.map((s, i) => (
                        <TableRow key={i}>
                          <TableCell>{s.name}</TableCell>
                          <TableCell>{s.version || "Unavailable"}</TableCell>
                        </TableRow>
                      ))}
                    </TableBody>
                  </Table>
                  {!software.length && (
                    <Empty>No matching applications reported.</Empty>
                  )}
                </Panel>
                <Panel title="Discovered runtimes">
                  {inventory?.Inventory.runtimes.length ? (
                    <Fields
                      rows={inventory.Inventory.runtimes.map((r) => [
                        r.name,
                        r.version,
                      ])}
                    />
                  ) : (
                    <Empty>No runtime inventory available.</Empty>
                  )}
                </Panel>
              </>
            )}
            {page === "Deployments" && (
              <>
                <Alert severity="info">
                  {data?.configuration?.SimulatedDeployments
                    ? "Signed development simulations are enabled. No software is downloaded or installed."
                    : "Simulated deployments are disabled. All new deployment jobs are rejected."}
                </Alert>
                <Panel title="Simulation history">
                  {data?.deployments?.length ? (
                    data.deployments.map((event) => (
                      <Box key={event.id} mb={2}>
                        <Stack direction="row" spacing={1} alignItems="center">
                          <Chip size="small" label={event.status.state} />
                          <Typography>{event.status.progress}%</Typography>
                        </Stack>
                        <Typography mt={1}>{event.status.message}</Typography>
                        <Typography variant="caption" color="text.secondary">
                          {event.jobId} · {timestamp(event.status.updatedAt)} ·
                          Report {event.delivery}
                        </Typography>
                      </Box>
                    ))
                  ) : (
                    <Empty>No simulation history recorded.</Empty>
                  )}
                </Panel>
                <Panel title="Recent job decisions">
                  {data?.jobs.length ? (
                    data.jobs.map((j) => (
                      <Box key={j.id} mb={3}>
                        <Stack direction="row" spacing={1} alignItems="center">
                          <Typography fontWeight={600}>
                            {j.kind} · {j.targetVersion || "No target version"}
                          </Typography>
                          <Chip
                            size="small"
                            label={j.accepted ? "Accepted" : "Rejected"}
                          />
                        </Stack>
                        <Typography color="text.secondary" mt={1}>
                          {j.reason}
                        </Typography>
                        <Typography variant="caption" color="text.secondary">
                          {j.id} ·{" "}
                          {j.reported
                            ? "Acknowledged"
                            : "Awaiting acknowledgement"}
                        </Typography>
                      </Box>
                    ))
                  ) : (
                    <Empty>No job decisions recorded.</Empty>
                  )}
                </Panel>
              </>
            )}
            {page === "Updates" && (
              <>
                <Alert severity="info">
                  {data?.configuration?.SimulatedUpdates
                    ? "Independent update simulations are enabled. No executable or Windows service is changed."
                    : "New update simulations are disabled. Existing recovery history is shown below."}
                </Alert>
                <Panel title="Simulated recovery target">
                  <Fields
                    rows={[
                      [
                        "Current simulated version",
                        data?.updateTarget?.current,
                      ],
                      [
                        "Known-good simulated version",
                        data?.updateTarget?.knownGood,
                      ],
                    ]}
                  />
                  {data?.updateTarget &&
                    data.updateTarget.current !==
                      data.updateTarget.knownGood && (
                      <Alert severity="warning">
                        The simulated target differs from known-good. Recovery
                        may be active or require operator attention.
                      </Alert>
                    )}
                </Panel>
                <Panel title="Update simulation history">
                  {data?.updates?.length ? (
                    data.updates.map((event) => (
                      <Box key={event.id} mb={2}>
                        <Stack direction="row" spacing={1} alignItems="center">
                          <Chip size="small" label={event.status.state} />
                          <Typography>{event.status.targetVersion}</Typography>
                        </Stack>
                        <Typography mt={1}>{event.status.message}</Typography>
                        <Typography variant="caption" color="text.secondary">
                          {event.jobId} · {timestamp(event.status.updatedAt)} ·{" "}
                          {event.delivered
                            ? "Report delivered"
                            : "Report pending"}
                        </Typography>
                      </Box>
                    ))
                  ) : (
                    <Empty>No update simulation history recorded.</Empty>
                  )}
                </Panel>
              </>
            )}
            {page === "Configuration" && (
              <>
                <Panel title="Desired kiosk configuration">
                  {policy ? (
                    <Fields
                      rows={[
                        ["Revision", policy.revision],
                        [
                          "Executable",
                          policy.kioskExecutable || "Not configured",
                        ],
                        [
                          "Expected shell",
                          policy.expectedShell || "Not configured",
                        ],
                        [
                          "Expected startup entries",
                          policy.startupEntries.join("\n") || "None specified",
                        ],
                        [
                          "Auto-launch requested",
                          policy.autoLaunch ? "Yes" : "No",
                        ],
                        ["Restart policy", policy.restartPolicy],
                        [
                          "Desired monitor interval",
                          `${policy.monitorIntervalSeconds}s`,
                        ],
                        [
                          "Effective collection interval",
                          data?.configuration
                            ? `${data.configuration.IntervalSeconds}s`
                            : "Unavailable",
                        ],
                      ]}
                    />
                  ) : (
                    <Empty>No policy has been received.</Empty>
                  )}
                </Panel>
                <Panel title="Read-only comparison">
                  {report ? (
                    <>
                      <Chip
                        label={report.compliance}
                        color={
                          report.compliance === "compliant"
                            ? "success"
                            : report.compliance === "drifted"
                              ? "warning"
                              : "default"
                        }
                      />
                      <Typography variant="caption" display="block" my={2}>
                        Revision {report.revision} ·{" "}
                        {timestamp(report.observedAt)}
                      </Typography>
                      {report.drift.map((v, i) => (
                        <Alert key={i} severity="warning" sx={{ mb: 1 }}>
                          {v}
                        </Alert>
                      ))}
                      {report.errors.map((v, i) => (
                        <Alert key={i} severity="info" sx={{ mb: 1 }}>
                          {v}
                        </Alert>
                      ))}
                    </>
                  ) : (
                    <Empty>No comparison available.</Empty>
                  )}
                </Panel>
              </>
            )}
            {page === "Logs" && (
              <Panel title="Recent service events">
                <Typography color="text.secondary" variant="body2" mb={2}>
                  Last 100 messages from this service process. Detailed
                  attributes stay in the service log.
                </Typography>
                {snapshot?.logs.length ? (
                  snapshot.logs
                    .slice()
                    .reverse()
                    .map((l, i) => (
                      <Box
                        key={i}
                        py={1.5}
                        sx={{ borderBottom: 1, borderColor: "divider" }}
                      >
                        <Typography variant="caption" color="text.secondary">
                          {timestamp(l.time)} · {l.level}
                        </Typography>
                        <Typography variant="body2">{l.message}</Typography>
                      </Box>
                    ))
                ) : (
                  <Empty>No service events available.</Empty>
                )}
              </Panel>
            )}
            {page === "Maintenance" && (
              <Maintenance
                connected={connected}
                enabled={snapshot?.maintenanceEnabled ?? false}
              />
            )}
            {page === "About" && (
              <Panel title="Kiosentra agent">
                <Fields
                  rows={[
                    ["Version", snapshot?.version],
                    ["Commit", snapshot?.commit],
                    ["API contract", "v1"],
                    ["Console", "Wails v3 · React · Material UI"],
                    ["Mode", "Read-only monitoring with scoped maintenance"],
                    [
                      "Window behavior",
                      "Closing hides the console. The device service keeps running.",
                    ],
                    ["Default shortcut", "Ctrl + Alt + Shift + F12"],
                  ]}
                />
              </Panel>
            )}
            <Divider />
            <Typography variant="caption" color="text.secondary">
              Kiosentra · Local device console · Data is collected by the
              background service
            </Typography>
          </Stack>
        </Box>
      </Box>
    </ThemeProvider>
  );
}
function Maintenance({
  connected,
  enabled,
}: {
  connected: boolean;
  enabled: boolean;
}) {
  const [secret, setSecret] = useState(""),
    [scope, setScope] = useState("refresh_inventory"),
    [grant, setGrant] = useState<Grant | null>(null),
    [busy, setBusy] = useState(false),
    [notice, setNotice] = useState(""),
    [error, setError] = useState(""),
    [confirm, setConfirm] = useState(false),
    [now, setNow] = useState(Date.now());
  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, []);
  const authorizationEpoch = useRef(0);
  useEffect(() => {
    authorizationEpoch.current++;
    if (!connected || !enabled) {
      setGrant(null);
      setSecret("");
      setConfirm(false);
    }
    return () => {
      authorizationEpoch.current++;
    };
  }, [connected, enabled]);
  const valid =
    grant && new Date(grant.expiresAt).getTime() > now && grant.scope === scope;
  const authorize = async () => {
    const epoch = authorizationEpoch.current;
    setBusy(true);
    setError("");
    setNotice("");
    try {
      const result = await api.authorize(secret, scope);
      if (epoch === authorizationEpoch.current) setGrant(result);
    } catch (e) {
      setGrant(null);
      setError(errorText(e));
    } finally {
      setSecret("");
      setBusy(false);
    }
  };
  const act = async () => {
    setConfirm(false);
    if (!grant || !valid || !connected || !enabled || busy) return;
    setBusy(true);
    setError("");
    try {
      await api.action(grant.token, scope);
      setNotice("Authorized request sent to the service.");
    } catch (e) {
      setError(errorText(e));
    } finally {
      setGrant(null);
      setBusy(false);
    }
  };
  return (
    <>
      <Alert severity="info">
        Viewing the console does not grant maintenance access. Authorizations
        last two minutes and allow one action.
      </Alert>
      <Panel title="Technician authorization">
        {!enabled && (
          <Alert severity="warning" sx={{ mb: 2 }}>
            Maintenance authentication has not been provisioned on the service.
          </Alert>
        )}
        <Stack spacing={2}>
          <FormControl fullWidth>
            <InputLabel id="scope-label">Action</InputLabel>
            <Select
              labelId="scope-label"
              label="Action"
              value={scope}
              disabled={busy || !connected || !enabled}
              onChange={(e) => {
                authorizationEpoch.current++;
                setScope(e.target.value);
                setGrant(null);
              }}
            >
              <MenuItem value="refresh_inventory">Refresh inventory</MenuItem>
              <MenuItem value="stop_service">Stop device service</MenuItem>
              <MenuItem value="cancel_deployment">
                Cancel active simulation
              </MenuItem>
            </Select>
          </FormControl>
          <TextField
            type="password"
            label="Technician secret"
            autoComplete="off"
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            disabled={!connected || !enabled || busy}
          />
          <Button
            variant="outlined"
            onClick={() => void authorize()}
            disabled={!connected || !enabled || busy || !secret}
          >
            {busy ? (
              <CircularProgress size={20} />
            ) : (
              "Authorize selected action"
            )}
          </Button>
          {grant && (
            <Alert severity={valid ? "success" : "warning"}>
              {valid
                ? `Authorized until ${timestamp(grant.expiresAt)}`
                : "Authorization expired. Authenticate again."}
            </Alert>
          )}
          <Button
            variant="contained"
            color={scope === "stop_service" ? "error" : "primary"}
            disabled={!connected || !enabled || !valid || busy}
            onClick={() =>
              scope === "stop_service" ? setConfirm(true) : void act()
            }
          >
            {scope === "stop_service"
              ? "Stop device service"
              : scope === "cancel_deployment"
                ? "Cancel active simulation"
                : "Refresh inventory"}
          </Button>
          {error && <Alert severity="error">{error}</Alert>}
          {notice && <Alert severity="success">{notice}</Alert>}
        </Stack>
      </Panel>
      <Dialog open={confirm} onClose={() => setConfirm(false)}>
        <DialogTitle>Stop the device service?</DialogTitle>
        <DialogContent>
          Monitoring and reporting will stop. Closing the console alone keeps
          the service running. A local administrator must restart the service.
        </DialogContent>
        <DialogActions>
          <Button onClick={() => setConfirm(false)}>Cancel</Button>
          <Button
            color="error"
            disabled={!valid || !connected || !enabled || busy}
            onClick={() => void act()}
          >
            Confirm stop
          </Button>
        </DialogActions>
      </Dialog>
    </>
  );
}
