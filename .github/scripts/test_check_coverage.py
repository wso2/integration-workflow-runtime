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

import tempfile
import unittest

from check_coverage import coverage, main

PROFILE = """mode: set
example.com/m/internal/a/a.go:1.1,3.2 4 1
example.com/m/internal/a/a.go:4.1,6.2 4 0
example.com/m/cmd/tool/main.go:1.1,3.2 10 0
"""


class CheckCoverageTest(unittest.TestCase):
    def test_excludes_cmd_wrappers(self):
        self.assertEqual(coverage(PROFILE.splitlines()), (4, 8))

    def test_counts_repeated_blocks_once(self):
        lines = PROFILE.splitlines() + ["example.com/m/internal/a/a.go:4.1,6.2 4 1"]
        self.assertEqual(coverage(lines), (8, 8))

    def test_main_enforces_minimum(self):
        with tempfile.NamedTemporaryFile("w", suffix=".out", delete=False) as f:
            f.write(PROFILE)
        self.assertEqual(main([f.name, "--minimum", "50"]), 0)
        self.assertEqual(main([f.name, "--minimum", "75"]), 1)

    def test_main_fails_on_empty_profile(self):
        with tempfile.NamedTemporaryFile("w", suffix=".out", delete=False) as f:
            f.write("mode: set\n")
        self.assertEqual(main([f.name]), 1)


if __name__ == "__main__":
    unittest.main()
