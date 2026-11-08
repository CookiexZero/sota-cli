#!/usr/bin/env python3
"""Verify HTTPS to one domain using a real Sota key; never modify host networking."""

import argparse
import json
import pathlib
import subprocess
import time
import uuid


ROOT = pathlib.Path(__file__).resolve().parents[1]


def capture(*args):
    return subprocess.check_output(args, cwd=ROOT, text=True).strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", choices=["all", "amd64", "arm64"], default="all")
    parser.add_argument("--mode", choices=["all", "proxy", "tun"], default="all")
    parser.add_argument("--domain", default="example.com")
    parser.add_argument("--env-file", type=pathlib.Path, default=ROOT / ".env")
    parser.add_argument("--scope", choices=["domain", "all"], default="domain")
    parser.add_argument("--image", default="sota-cli:dev")
    args = parser.parse_args()
    arches = ["amd64", "arm64"] if args.arch == "all" else [args.arch]
    modes = ["proxy", "tun"] if args.mode == "all" else [args.mode]
    state = ROOT / ".cache/live-state"
    state.mkdir(parents=True, exist_ok=True, mode=0o700)
    for arch in arches:
        for mode in modes:
            name = f"sota-live-{arch}-{mode}-{uuid.uuid4().hex[:8]}"
            print(f"Testing real Sota: linux/{arch}, {mode}, domain={args.domain}", flush=True)
            command = ["docker", "run", "-d", "--name", name, "--platform", "linux/" + arch, "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true", "--tmpfs", "/run/sota-cli", "--tmpfs", "/tmp"]
            if mode == "tun":
                command += ["--device", "/dev/net/tun", "--cap-add", "NET_ADMIN"]
            command += ["--mount", f"type=bind,src={args.env_file.resolve()},dst=/run/secrets/sota-env,readonly", "--mount", f"type=bind,src={state},dst=/var/lib/sota-cli", args.image, "run", "--env-file", "/run/secrets/sota-env", "--mode", mode, "--transport", "vless", "--timeout", "45s"]
            command += ["--all"] if args.scope == "all" else ["--domain", args.domain]
            try:
                capture(*command)
                status = None
                for _ in range(100):
                    if capture("docker", "inspect", "-f", "{{.State.Running}}", name) != "true":
                        raise RuntimeError("Sota container exited; inspect its sanitized logs")
                    result = subprocess.run(["docker", "exec", name, "sota", "status", "--json"], capture_output=True, text=True)
                    if result.returncode == 0:
                        status = json.loads(result.stdout)
                        if status["state"] == 2:
                            break
                    time.sleep(0.2)
                if not status or status["state"] != 2:
                    raise RuntimeError("Sota did not become connected")
                probe = ["docker", "run", "--rm", "--network", "container:" + name, "--cap-drop", "ALL", "--mount", f"type=bind,src={ROOT / 'tests/live_request.py'},dst=/probe.py,readonly", "python:3.13-alpine", "python", "/probe.py", "--domain", args.domain, "--mode", mode]
                if mode == "proxy":
                    probe += ["--port", str(status["proxy_port"])]
                subprocess.run(probe, cwd=ROOT, check=True, timeout=60)
                subprocess.run(["docker", "exec", name, "sota", "health"], check=True)
            finally:
                subprocess.run(["docker", "stop", "--timeout", "10", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
