#!/usr/bin/env python3
"""Build both architectures and exercise the shipped cores in isolated containers."""

import argparse
import pathlib
import subprocess
import tempfile


ROOT = pathlib.Path(__file__).resolve().parents[1]


def run(*args):
    subprocess.run(args, cwd=ROOT, check=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--arch", choices=["all", "amd64", "arm64"], default="all")
    parser.add_argument("--mode", choices=["all", "proxy", "tun"], default="all")
    parser.add_argument("--skip-build", action="store_true")
    parser.add_argument("--scope", choices=["both", "domain", "all"], default="both")
    parser.add_argument("--image", default="sota-cli:dev")
    args = parser.parse_args()
    arches = ["amd64", "arm64"] if args.arch == "all" else [args.arch]
    modes = ["proxy", "tun"] if args.mode == "all" else [args.mode]
    scopes = ["domain", "all"] if args.scope == "both" else [args.scope]
    run("docker", "run", "--rm", "--mount", f"type=bind,src={ROOT},dst=/src", "-w", "/src", "golang:1.25-alpine", "sh", "-c", "go test ./... && go vet ./...")
    if not args.skip_build:
        run("docker", "buildx", "build", "--platform", ",".join("linux/" + arch for arch in arches), "--load", "-t", args.image, ".")
    for arch in arches:
        with tempfile.TemporaryDirectory(prefix="sota-test-") as directory:
            work = pathlib.Path(directory)
            container = subprocess.check_output(["docker", "create", "--platform", "linux/" + arch, "--network", "none", args.image, "version"], text=True).strip()
            try:
                for filename in ("sota", "kittend"):
                    run("docker", "cp", f"{container}:/usr/local/bin/{filename}", str(work / filename))
            finally:
                run("docker", "rm", container)
            for mode, scope in [(mode, scope) for mode in modes for scope in scopes]:
                print(f"Testing linux/{arch}, mode={mode}, scope={scope}", flush=True)
                command = ["docker", "run", "--rm", "--platform", "linux/" + arch, "--cap-drop", "ALL", "--security-opt", "no-new-privileges:true"]
                if mode == "proxy":
                    command += ["--network", "none"]
                else:
                    command += ["--network", "bridge", "--device", "/dev/net/tun", "--cap-add", "NET_ADMIN"]
                command += ["-e", "SOTA_TEST_MODE=" + mode]
                command += ["-e", "SOTA_TEST_SCOPE=" + scope]
                for source, target in [(work / "sota", "/sota"), (work / "kittend", "/kittend"), (ROOT / "tests/integration.py", "/integration.py")]:
                    command += ["--mount", f"type=bind,src={source},dst={target},readonly"]
                command += ["python:3.13-alpine", "python", "-u", "/integration.py"]
                run(*command)


if __name__ == "__main__":
    main()
