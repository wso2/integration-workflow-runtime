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

"""Checks a commit subject or PR title against Conventional Commits, as ballerina-nutcracker does."""

from __future__ import annotations

import re
import sys

ALLOWED_TYPES = ("feat", "fix", "build", "chore", "ci", "docs", "style", "refactor", "perf", "test", "revert")
MAX_SUBJECT_LENGTH = 72
EXPECTED_FORMAT = "<type>(<optional scope>): <description>"

_SUBJECT_PATTERN = re.compile(rf"^(?:{'|'.join(ALLOWED_TYPES)})(?:\([^)]+\))?!?: [a-z].*$")


def validation_errors(subject: str) -> list[str]:
    errors = []
    if not _SUBJECT_PATTERN.fullmatch(subject):
        errors.append("does not match the Conventional Commits format")
    if len(subject) > MAX_SUBJECT_LENGTH:
        errors.append(f"is {len(subject)} characters, exceeding the {MAX_SUBJECT_LENGTH}-character limit")
    if subject.endswith("."):
        errors.append("ends with a period")
    return errors


def describe_rules() -> str:
    return "\n".join((
        f"Expected: {EXPECTED_FORMAT}",
        f"Allowed types: {', '.join(ALLOWED_TYPES)}",
        "The scope is optional and unrestricted.",
        "A breaking-change marker (!) before the colon is optional.",
        "The description starts with a lowercase letter, in the imperative mood, without a final period.",
        f"Maximum length: {MAX_SUBJECT_LENGTH} characters.",
    ))


def main(arguments: list[str]) -> int:
    if len(arguments) != 1:
        print("Error: expected exactly one PR title or commit subject argument.\n\n" + describe_rules(), file=sys.stderr)
        return 2
    subject = arguments[0]
    errors = validation_errors(subject)
    if errors:
        print(f'Invalid title or subject "{subject}":\n' + "\n".join(f"- {e}" for e in errors) + "\n\n" + describe_rules(),
              file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
