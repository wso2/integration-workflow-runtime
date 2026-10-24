#!/usr/bin/env python3
# Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
#
# WSO2 LLC. licenses this file to you under the Apache License,
# Version 2.0 (the "License"); you may not use this file except
# in compliance with the License.
# You may obtain a copy of the License at
#
# http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing,
# software distributed under the License is distributed on an
# "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
# KIND, either express or implied.  See the License for the
# specific language governing permissions and limitations
# under the License.

"""Runs a Ballerina test suite (smoke or full) against a freshly started workflow-engine binary.

Starts the engine on tests/engine/temporal-test.yaml, waits until its frontend is SERVING, creates the
`default` namespace and the search attributes ballerina/workflow uses with the Temporal CLI, runs `bal test` in tests/ballerina/<suite>, then stops the engine.
"""

from __future__ import annotations

import argparse
import os
import signal
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
ENGINE_CONFIG = ROOT / "tests" / "engine" / "temporal-test.yaml"
ADDRESS = "localhost:17233"
NAMESPACE = "default"
# ballerina/workflow tags every instance with this attribute; a fresh namespace does not define it.
SEARCH_ATTRIBUTES = {"WorkflowKind": "Keyword"}
SUITES = ("smoke", "full")


def run_quiet(command: list[str]) -> subprocess.CompletedProcess:
    return subprocess.run(command, capture_output=True, text=True, check=False)


def wait_until_serving(temporal: str, engine: subprocess.Popen, timeout: float) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if engine.poll() is not None:
            raise RuntimeError(f"workflow-engine exited with code {engine.returncode} before it was ready")
        result = run_quiet([temporal, "operator", "cluster", "health", "--address", ADDRESS])
        if result.returncode == 0 and "SERVING" in result.stdout:
            return
        time.sleep(1)
    raise RuntimeError(f"workflow-engine was not SERVING on {ADDRESS} after {timeout:.0f}s")


def ensure_namespace(temporal: str, timeout: float) -> None:
    create = run_quiet([temporal, "operator", "namespace", "create", "--namespace", NAMESPACE,
                        "--retention", "24h", "--address", ADDRESS])
    if create.returncode != 0 and "already" not in (create.stdout + create.stderr).lower():
        raise RuntimeError(f"could not create namespace {NAMESPACE}: {create.stderr.strip()}")
    deadline = time.monotonic() + timeout
    while run_quiet([temporal, "operator", "namespace", "describe", "--namespace", NAMESPACE,
                     "--address", ADDRESS]).returncode != 0:
        if time.monotonic() > deadline:
            raise RuntimeError(f"namespace {NAMESPACE} did not become available")
        time.sleep(1)
    for name, kind in SEARCH_ATTRIBUTES.items():
        added = run_quiet([temporal, "operator", "search-attribute", "create", "--namespace", NAMESPACE,
                           "--name", name, "--type", kind, "--address", ADDRESS])
        if added.returncode != 0 and "already" not in (added.stdout + added.stderr).lower():
            raise RuntimeError(f"could not add search attribute {name}: {added.stderr.strip()}")


def stop(engine: subprocess.Popen) -> None:
    if engine.poll() is None:
        engine.send_signal(signal.SIGTERM if os.name != "nt" else signal.CTRL_BREAK_EVENT)
        try:
            engine.wait(timeout=30)
        except subprocess.TimeoutExpired:
            engine.kill()
            engine.wait()


def print_log_tail(log_path: Path, lines: int = 80) -> None:
    content = log_path.read_text(encoding="utf-8", errors="replace").splitlines()
    print(f"--- last {min(lines, len(content))} lines of {log_path} ---", file=sys.stderr)
    for line in content[-lines:]:
        print(line, file=sys.stderr)


def main(arguments: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("suite", choices=SUITES)
    parser.add_argument("--engine", required=True, help="path to the workflow-engine binary")
    parser.add_argument("--bal", default=os.environ.get("BAL", "bal"), help="jBallerina `bal` command")
    parser.add_argument("--temporal", default=os.environ.get("TEMPORAL_CLI", "temporal"), help="Temporal CLI")
    parser.add_argument("--startup-timeout", type=float, default=90)
    args = parser.parse_args(arguments)

    suite_dir = ROOT / "tests" / "ballerina" / args.suite
    log_dir = Path(os.environ.get("ENGINE_LOG_DIR", tempfile.mkdtemp(prefix="workflow-engine-")))
    log_dir.mkdir(parents=True, exist_ok=True)
    log_path = log_dir / f"engine-{args.suite}.log"

    with log_path.open("w", encoding="utf-8") as log:
        engine = subprocess.Popen([str(Path(args.engine).resolve()), "start", "--config-file", str(ENGINE_CONFIG),
                                   "--allow-no-auth"], stdout=log, stderr=subprocess.STDOUT)
    try:
        wait_until_serving(args.temporal, engine, args.startup_timeout)
        ensure_namespace(args.temporal, 30)
        print(f"workflow-engine SERVING on {ADDRESS}; running the {args.suite} suite", flush=True)
        code = subprocess.run([args.bal, "test"], cwd=suite_dir, check=False).returncode
    except RuntimeError as error:
        print(f"error: {error}", file=sys.stderr)
        code = 1
    finally:
        stop(engine)
    if code != 0:
        print_log_tail(log_path)
    return code


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
