#!/usr/bin/env python3
"""Actual Agent + persistent central simulation jobs, restart, cancellation and retry.

All keys/state are temporary. Build Agent/server first; Go is needed for keygen.
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
import uuid

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
    keys = json.loads(subprocess.check_output(
        ["go", "run", "./tools/mock-server/cmd/simulation-keygen"], cwd=ROOT))
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    origin = f"http://127.0.0.1:{port}"
    env = {k: v for k, v in os.environ.items() if not k.startswith("KIOSENTRA_")}
    env.update(
        KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN=secrets.token_hex(32),
        KIOSENTRA_CENTRAL_ADMIN_TOKEN=secrets.token_hex(32),
        KIOSENTRA_CENTRAL_CREDENTIAL_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
        KIOSENTRA_CENTRAL_SIMULATION_PRIVATE_KEY=keys["privateKey"],
    )
    server = agent = None
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def api(path, body=None, key=None):
        req = urllib.request.Request(origin + "/api/v1/admin/" + path,
                                     data=None if body is None else json.dumps(body).encode())
        req.add_header("Authorization", "Bearer " + env["KIOSENTRA_CENTRAL_ADMIN_TOKEN"])
        if body is not None:
            req.add_header("Content-Type", "application/json")
            req.add_header("Idempotency-Key", key or str(uuid.uuid4()))
        with opener.open(req, timeout=2) as response:
            return json.load(response)

    def wait_for(check, label):
        deadline = time.monotonic() + 60
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
            time.sleep(0.1)
        raise RuntimeError(f"timed out waiting for {label}")

    with tempfile.TemporaryDirectory(prefix="kiosentra-jobs-smoke-") as directory:
        state = Path(directory)
        server_command = [str(ROOT / "bin/kiosentra-server"), "--listen", f"127.0.0.1:{port}",
                          "--database", str(state / "central.db")]
        agent_command = [str(ROOT / "bin/kiosentra-agent"), "--mode=console"]
        with (state / "server.log").open("wb") as server_log, (state / "agent.log").open("wb") as agent_log:
            try:
                server = subprocess.Popen(server_command, env=env, stdout=server_log, stderr=server_log)
                wait_for(lambda: api("devices"), "central startup")
                # The Agent never receives the server signing key or admin credential.
                agent_env = {k: v for k, v in env.items() if not k.startswith("KIOSENTRA_")}
                agent_env.update(
                    KIOSENTRA_SERVER_URL=origin, KIOSENTRA_DEVELOPMENT="true",
                    KIOSENTRA_ENROLLMENT_TOKEN=env["KIOSENTRA_CENTRAL_ENROLLMENT_TOKEN"],
                    KIOSENTRA_DEVELOPMENT_KEY=base64.b64encode(secrets.token_bytes(32)).decode(),
                    KIOSENTRA_STATE_DIR=str(state / "agent"), KIOSENTRA_INTERVAL="1s",
                    KIOSENTRA_SIMULATED_DEPLOYMENTS="true", KIOSENTRA_SIMULATION_PUBLIC_KEY=keys["publicKey"],
                )
                agent = subprocess.Popen(agent_command, env=agent_env, stdout=agent_log, stderr=agent_log)
                devices = wait_for(lambda: api("devices")["items"], "enrollment")
                device = devices[0]["deviceId"]

                def create(scenario="success"):
                    return api("jobs", {"deviceId": device, "targetVersion": "2.0-test",
                                        "scenario": scenario, "leaseSeconds": 300})

                def state_is(job, desired):
                    record = api("jobs/" + job["job"]["id"])
                    return record if record["state"] in desired else None

                job = create()
                wait_for(lambda: state_is(job, {"downloading"}), "first running checkpoint")
                # Stop both processes at a persisted safe point; resume without enrollment.
                stop(agent)
                stop(server)
                agent = server = None
                agent_env.pop("KIOSENTRA_ENROLLMENT_TOKEN")
                server = subprocess.Popen(server_command, env=env, stdout=server_log, stderr=server_log)
                wait_for(lambda: api("devices"), "central restart")
                agent = subprocess.Popen(agent_command, env=agent_env, stdout=agent_log, stderr=agent_log)
                complete = wait_for(lambda: state_is(job, {"succeeded"}), "resumed simulation completion")
                states = [e["payload"]["state"] for e in reversed(complete["events"])
                          if e["path"].endswith("/status")]
                assert states == ["accepted", "downloading", "verifying", "installing", "validating", "succeeded"], states

                cancel = create()
                wait_for(lambda: state_is(cancel, {"downloading"}), "cancellation checkpoint")
                path = "jobs/" + cancel["job"]["id"]
                result = api(path + "/cancel", {})
                assert result["state"] == "cancellation_requested", result["state"]
                cancelled = wait_for(lambda: state_is(cancel, {"cancelled"}), "Agent cancellation confirmation")
                assert cancelled["status"]["state"] == "cancelled"
                key = str(uuid.uuid4())
                retry = api(path + "/retry", {}, key)
                replay = api(path + "/retry", {}, key)
                assert retry["job"]["id"] == replay["job"]["id"]
                assert retry["retryOf"] == cancel["job"]["id"]
                wait_for(lambda: state_is(retry, {"succeeded"}), "retry completion")
                failure = create("hash_failure")
                failed = wait_for(lambda: state_is(failure, {"failed"}), "simulation failure")
                assert "SHA-256" in failed["status"]["message"]
                assert len(api("jobs?deviceId=" + device)["items"]) == 4
                print("PASS: signed simulation, persisted restart, Agent-confirmed cancellation, idempotent retry and failure history")
            finally:
                stop(agent)
                stop(server)


if __name__ == "__main__":
    run()
