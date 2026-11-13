#!/usr/bin/env python3
"""Real-binary Agent/central restart smoke; all state and secrets are temporary.

Build bin/kiosentra-agent and bin/kiosentra-server first. No external services needed.
"""
import base64
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[1]


def stop(process):
    if process is not None and process.poll() is None:
        process.terminate()
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def run():
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    # Do not inherit unrelated Kiosentra settings from the invoking shell.
    env = {k: v for k, v in os.environ.items() if not k.startswith("KIOSENTRA_")}
    env.update(
        KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN=secrets.token_hex(32),
        KIOSENTRA_CENTRAL_ADMIN_TOKEN=secrets.token_hex(32),
        KIOSENTRA_CENTRAL_CREDENTIAL_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
    )
    server = agent = None
    # Avoid proxy settings affecting local-only acceptance traffic.
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def get(path):
        req = urllib.request.Request(origin + "/api/v1/admin/" + path)
        req.add_header("Authorization", "Bearer " + env["KIOSENTRA_CENTRAL_ADMIN_TOKEN"])
        with opener.open(req, timeout=2) as response:
            return json.load(response)

    def wait_for(check, label):
        deadline = time.monotonic() + 40
        while time.monotonic() < deadline:
            for process in (server, agent):
                if process is not None and process.poll() is not None:
                    raise RuntimeError(f"process exited during {label}: {process.returncode}")
            try:
                result = check()
                if result:
                    return result
            except (urllib.error.URLError, TimeoutError, ConnectionError):
                pass
            time.sleep(0.2)
        raise RuntimeError(f"timed out waiting for {label}")

    with tempfile.TemporaryDirectory(prefix="kiosentra-central-smoke-") as directory:
        state = Path(directory)
        server_command = [str(ROOT / "bin/kiosentra-server"),
                          "--listen", f"127.0.0.1:{port}",
                          "--database", str(state / "central.db")]
        with (state / "server.log").open("wb") as server_log, (state / "agent.log").open("wb") as agent_log:
            try:
                server = subprocess.Popen(server_command, env=env, stdout=server_log, stderr=server_log)
                wait_for(lambda: get("devices"), "central startup")
                agent_env = env.copy()
                agent_env.update(
                    KIOSENTRA_SERVER_URL=origin,
                    KIOSENTRA_DEVELOPMENT="true",
                    KIOSENTRA_ENROLLMENT_TOKEN=env["KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN"],
                    KIOSENTRA_DEVELOPMENT_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
                    KIOSENTRA_STATE_DIR=str(state / "agent"),
                    KIOSENTRA_INTERVAL="1s",
                )
                agent_command = [str(ROOT / "bin/kiosentra-agent"), "--mode=console"]
                agent = subprocess.Popen(agent_command, env=agent_env, stdout=agent_log, stderr=agent_log)
                devices = wait_for(lambda: get("devices")["items"], "device enrollment")
                assert len(devices) == 1, "expected one device"
                device_id = devices[0]["deviceId"]

                def complete_detail():
                    detail = get("devices/" + device_id)
                    return detail if all(detail[k] for k in ("heartbeat", "metrics", "inventory")) else None

                wait_for(complete_detail, "telemetry and inventory")
                # Stop the Agent to obtain a stable history boundary, then restart it
                # without a bootstrap token while central is unavailable.
                stop(agent)
                before = get("devices/" + device_id + "/reports?limit=100")["items"]
                assert before, "missing history"
                stop(server)
                agent_env.pop("KIOSENTRA_ENROLLMENT_TOKEN")
                agent = subprocess.Popen(agent_command, env=agent_env, stdout=agent_log, stderr=agent_log)
                time.sleep(1.5)
                server = subprocess.Popen(server_command, env=env, stdout=server_log, stderr=server_log)
                wait_for(lambda: get("devices"), "central restart")
                last_sequence = before[-1]["sequence"]
                wait_for(lambda: get(f"devices/{device_id}/reports?cursor={last_sequence}")["items"],
                         "reporting with saved identity after restart")
                after = get("devices/" + device_id + "/reports?limit=100")["items"]
                assert after[:len(before)] == before, "history changed after restart"
                devices = get("devices")["items"]
                assert len(devices) == 1 and devices[0]["deviceId"] == device_id, "identity changed"
                print("PASS: real Agent and central restart; same identity, retained history, resumed telemetry")
            finally:
                stop(agent)
                stop(server)


if __name__ == "__main__":
    run()
