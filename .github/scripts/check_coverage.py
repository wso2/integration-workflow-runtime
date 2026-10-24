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

"""Fails when Go statement coverage, outside the thin cmd/ wrappers, is below the required minimum."""

from __future__ import annotations

import argparse
import sys

EXCLUDED_SEGMENTS = ("/cmd/",)


def coverage(profile_lines: list[str]) -> tuple[int, int]:
    """Returns (covered, total) statements, counting each block once."""
    blocks: dict[str, tuple[int, bool]] = {}
    for line in profile_lines:
        line = line.strip()
        if not line or line.startswith("mode:"):
            continue
        location, statements, count = line.rsplit(" ", 2)
        if any(segment in location for segment in EXCLUDED_SEGMENTS):
            continue
        covered = int(count) > 0
        previous = blocks.get(location)
        blocks[location] = (int(statements), covered or (previous[1] if previous else False))
    total = sum(statements for statements, _ in blocks.values())
    hit = sum(statements for statements, covered in blocks.values() if covered)
    return hit, total


def main(arguments: list[str]) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("profile", nargs="+", help="Go coverage profiles (go test -coverprofile)")
    parser.add_argument("--minimum", type=float, default=75.0, help="required percentage")
    args = parser.parse_args(arguments)
    lines: list[str] = []
    for path in args.profile:
        with open(path, encoding="utf-8") as profile:
            lines.extend(profile.readlines())
    hit, total = coverage(lines)
    if total == 0:
        print("No statements found in the coverage profiles", file=sys.stderr)
        return 1
    percent = 100.0 * hit / total
    print(f"Coverage: {percent:.1f}% of {total} statements (minimum {args.minimum:.0f}%)")
    if percent < args.minimum:
        print(f"Coverage {percent:.1f}% is below the required {args.minimum:.0f}%", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
