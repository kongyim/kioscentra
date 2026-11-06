#!/usr/bin/env python3
"""POSIX real-binary simulation smoke test; uses only fresh temporary state.

Run scripts/check.sh first, then python3 scripts/phase6-smoke.py.
Requires Go (development signing-key generator) and Python 3. Uses SIGSTOP on
its own mock process to preserve enrollment while central is unresponsive.
"""

import argparse
import shutil
import base64
import json
import os
from pathlib import Path
import signal
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


ROOT = Path(__file__).resolve().parents[1]


def wait_for(check, label, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        value = check()
        if value:
            return value
        time.sleep(0.025)
    raise AssertionError("Timed out: " + label)


def stop(process):
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait()


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--protected-settings", action="store_true", help="also verify Phase 7 file provisioning, IPC status and separate recovery copy")
    args = parser.parse_args()
    if os.name != "posix":
        raise SystemExit("This smoke test requires POSIX signals; it is not Windows acceptance.")
    keys = json.loads(subprocess.check_output(
        ["go", "run", "./tools/mock-server/cmd/simulation-keygen"], cwd=ROOT))
    env = {k: v for k, v in os.environ.items() if not k.startswith("KIOSENTRA_")}
    token = base64.b64encode(os.urandom(32)).decode()
    enrollment = base64.b64encode(os.urandom(32)).decode()
    with socket.socket() as reservation:
        reservation.bind(("127.0.0.1", 0))
        port = reservation.getsockname()[1]
    origin = "http://127.0.0.1:" + str(port)

    def request(path, data=None):
        req = urllib.request.Request(origin + path,
            data=None if data is None else json.dumps(data).encode(),
            headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        with urllib.request.urlopen(req, timeout=1) as response:
            return json.load(response)

    with tempfile.TemporaryDirectory(prefix="kiosentra-phase6-") as directory:
        state = Path(directory) / "state"
        journal = state / "update-simulation" / "journal.db"
        mock = agent = None
        paused = False
        with open(Path(directory) / "process.log", "w+") as log:
            try:
                mock = subprocess.Popen([str(ROOT / "bin/mock-server"), "-listen", f"127.0.0.1:{port}"],
                    env={**env, "KIOSENTRA_MOCK_ENROLLMENT_TOKEN": enrollment,
                         "KIOSENTRA_MOCK_ADMIN_TOKEN": token,
                         "KIOSENTRA_MOCK_SIMULATION_PRIVATE_KEY": keys["privateKey"]},
                    stdout=log, stderr=log)
                agent_env = {**env, "KIOSENTRA_SERVER_URL": origin,
                    "KIOSENTRA_STATE_DIR": str(state), "KIOSENTRA_DEVELOPMENT": "true",
                    "KIOSENTRA_DEVELOPMENT_KEY": base64.b64encode(os.urandom(32)).decode(),
                    "KIOSENTRA_ENROLLMENT_TOKEN": enrollment, "KIOSENTRA_INTERVAL": "1s",
                    "KIOSENTRA_SIMULATED_UPDATES": "true",
                    "KIOSENTRA_SIMULATION_PUBLIC_KEY": keys["publicKey"]}

                command = [str(ROOT / "bin/kiosentra-agent"), "--mode=console"]
                client_config = None
                if args.protected_settings:
                    recovery = Path(directory) / "recovery-agent"
                    shutil.copy2(ROOT / "bin/kiosentra-agent", recovery)
                    settings = {k: v for k, v in agent_env.items() if k.startswith("KIOSENTRA_")}
                    settings["KIOSENTRA_IPC_KEY"] = base64.b64encode(os.urandom(32)).decode()
                    settings["KIOSENTRA_RECOVERY_EXECUTABLE"] = str(recovery)
                    technician_file = Path(directory) / "technicians.json"
                    technician_file.write_text(json.dumps({"alice": {"sha256": "0" * 64, "scopes": ["refresh_inventory"]}}))
                    technician_file.chmod(0o600)
                    settings["KIOSENTRA_TECHNICIANS_FILE"] = str(technician_file)
                    service_config = Path(directory) / "service.json"
                    service_config.write_text(json.dumps(settings))
                    service_config.chmod(0o600)
                    client_config = Path(directory) / "client.json"
                    client_config.write_text(json.dumps({k: settings[k] for k in ("KIOSENTRA_STATE_DIR", "KIOSENTRA_DEVELOPMENT", "KIOSENTRA_IPC_KEY")}))
                    client_config.chmod(0o600)
                    command += ["--config=" + str(service_config)]
                    # The explicit file must ignore a conflicting inherited server and credentials.
                    agent_env = {**env, "KIOSENTRA_SERVER_URL": "http://invalid.example", "KIOSENTRA_ENROLLMENT_TOKEN": "wrong"}

                def start_agent():
                    return subprocess.Popen(command, env=agent_env, stdout=log, stderr=log)

                def enrolled():
                    try:
                        return request("/dev/state")
                    except (OSError, urllib.error.URLError):
                        return None

                agent = start_agent()
                device = wait_for(enrolled, "enrollment")[0]["deviceId"]
                if client_config:
                    status = json.loads(subprocess.check_output([str(ROOT / "bin/kiosentra-agent"), "--mode=status", "--config=" + str(client_config)], env=agent_env))
                    assert status["deviceId"] == device and status["namedTechnicians"] and status["maintenanceEnabled"]
                    encoded = json.dumps(status)
                    assert settings["KIOSENTRA_IPC_KEY"] not in encoded and enrollment not in encoded
                job = request("/dev/jobs", {"deviceId": device, "kind": "agent_update",
                    "targetVersion": "phase6-smoke", "leaseSeconds": 300, "scenario": "health_timeout"})

                def record():
                    if not journal.exists():
                        return None
                    with sqlite3.connect(f"file:{journal}?mode=ro", uri=True) as db:
                        try:
                            row = db.execute("SELECT record FROM updates WHERE id=?", (job["id"],)).fetchone()
                        except sqlite3.OperationalError:
                            return None
                        return json.loads(row[0]) if row else None

                wait_for(lambda: (record() or {}).get("state") == "verifying", "candidate verification")
                mock.send_signal(signal.SIGSTOP)
                paused = True
                stop(agent)
                recovered = wait_for(lambda: (r if (r := record()) and r["state"] == "rolled_back" else None),
                    "detached recovery with service stopped and central paused")
                with sqlite3.connect(f"file:{journal}?mode=ro", uri=True) as db:
                    target = db.execute("SELECT current,known_good FROM target").fetchone()
                    assert target == (recovered["previous"], recovered["previous"]), target
                    assert db.execute("SELECT count(*) FROM events WHERE delivered=0").fetchone()[0] > 0
                mock.send_signal(signal.SIGCONT)
                paused = False
                agent = start_agent()

                def reports_delivered():
                    with sqlite3.connect(f"file:{journal}?mode=ro", uri=True) as db:
                        return db.execute("SELECT count(*) FROM events WHERE delivered=0").fetchone()[0] == 0

                wait_for(reports_delivered, "recovery reports after service restart")
                reports = [r for r in request("/dev/state")[0]["reports"] if r["path"] == "/agent/update-status"]
                assert len(reports) == 5, len(reports)
                print("PASS: real detached helper, offline rollback, known-good restoration, restart and five reports")
                if client_config:
                    print("PASS: isolated protected settings, named maintenance projection, redacted IPC status and separate recovery executable")
            finally:
                if paused:
                    mock.send_signal(signal.SIGCONT)
                stop(agent)
                stop(mock)


if __name__ == "__main__":
    main()
