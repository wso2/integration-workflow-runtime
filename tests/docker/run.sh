#!/usr/bin/env bash
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

# Runs inside the tests/docker runner: `run-tests [all|gates|smoke|full] ["sqlite postgresql mysql"]`.
# Works on a fresh copy of /src (tracked and untracked, not ignored files), so the host tree and build/ stay untouched.

set -euo pipefail

target="${1:-all}"
databases="${2:-sqlite postgresql mysql}"
case "$target" in all|gates|smoke|full) ;; *) echo "unknown target: $target (all, gates, smoke, full)" >&2; exit 2;; esac

rm -rf /work && mkdir -p /work /out
git config --global --add safe.directory /src
git config --global --add safe.directory /work
cp -a /src/.git /work/.git
(cd /src && git ls-files -z --cached --others --exclude-standard \
    | tar --null --ignore-failed-read -T - -cf - 2>/dev/null) | tar -xf - -C /work
cd /work
# The pinned Go tools live on a volume, so they are installed once, not on every run.
export TOOL_BIN=/opt/tools

if ! (cd engine && go mod download && go mod verify >/dev/null); then
    echo "The Go module cache volume is damaged. Reset the caches and rerun:" >&2
    echo "  docker compose -f tests/docker/compose.yml down --volumes" >&2
    exit 1
fi

run_suites() {
    make build
    for db in $databases; do
        echo "=== $1 suite on $db"
        ENGINE_LOG_DIR=/out go run ./tools/cmd/balsuite "$1" --database "$db" --engine build/workflow-engine
    done
}

if [[ "$target" == all || "$target" == gates ]]; then
    echo "=== CI gates (make check, make sbom)"
    make check sbom
fi
if [[ "$target" == all || "$target" == smoke ]]; then run_suites smoke; fi
if [[ "$target" == all || "$target" == full ]]; then run_suites full; fi
echo "=== passed: $target"
