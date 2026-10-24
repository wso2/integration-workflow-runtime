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

# Prints the next version (bare, e.g. "0.2.0") from the latest reachable release tag; used for nightly builds
# and as the default release version. Same rule as ballerina-nutcracker:
#   - no release tag yet: 0.1.0
#   - latest tag is a prerelease (has a "-" suffix): still mid-cycle, resume that base
#   - on a "release-X.Y.x" branch: patch + 1
#   - otherwise (e.g. main): minor + 1, patch reset to 0

set -euo pipefail

branch="${1:?usage: $0 <branch>}"
branch="${branch#refs/heads/}"

if ! latest_tag="$(git describe --tags --match 'v[0-9]*.[0-9]*.[0-9]*' --abbrev=0 2>/dev/null)"; then
    echo "0.1.0"
    exit 0
fi
bare="${latest_tag#v}"
base="${bare%%-*}"
major="$(echo "$base" | cut -d. -f1)"
minor="$(echo "$base" | cut -d. -f2)"
patch="$(echo "$base" | cut -d. -f3)"

if [[ "$bare" == *-* ]]; then
    echo "$base"
elif [[ "$branch" == "release-${major}.${minor}.x" ]]; then
    echo "${major}.${minor}.$((patch + 1))"
else
    echo "${major}.$((minor + 1)).0"
fi
