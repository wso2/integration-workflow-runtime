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

# Cuts a release: `make release VERSION=v0.1.0 [REMOTE=wso2]` tags the current commit and pushes the tag, which
# starts .github/workflows/release.yml. Without VERSION, the next version from resolve_next_version.sh is used.

set -euo pipefail

version="${1:-}"
remote="${2:-origin}"

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

if [[ -n "$(git status --porcelain)" ]]; then
    echo "refusing to release from a dirty worktree" >&2
    exit 1
fi
branch="$(git symbolic-ref --quiet --short HEAD || true)"
if [[ -z "$branch" ]]; then
    echo "refusing to release from a detached HEAD" >&2
    exit 1
fi
if [[ -z "$version" ]]; then
    version="v$(.github/scripts/resolve_next_version.sh "$branch")"
fi
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$ ]]; then
    echo "usage: $0 vMAJOR.MINOR.PATCH[-PRERELEASE] [remote]" >&2
    exit 1
fi
if git rev-parse --quiet --verify "refs/tags/$version" >/dev/null; then
    echo "tag $version already exists" >&2
    exit 1
fi

git tag -a "$version" -m "Release $version"
git push "$remote" "refs/tags/$version"
echo "Pushed $version to $remote; the Release workflow builds and publishes it."
