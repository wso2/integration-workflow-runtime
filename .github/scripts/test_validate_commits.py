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

import unittest

from validate_commits import main, validation_errors


class ValidateCommitsTest(unittest.TestCase):
    def test_accepts_conventional_subjects(self):
        for subject in ("feat: add engine endpoint", "fix(engine): refuse an open frontend",
                        "ci!: drop the old workflow", "docs(manager): describe sign-in"):
            with self.subTest(subject=subject):
                self.assertEqual(validation_errors(subject), [])

    def test_rejects_unknown_type(self):
        self.assertTrue(validation_errors("feature: add engine endpoint"))

    def test_rejects_uppercase_description(self):
        self.assertTrue(validation_errors("feat: Add engine endpoint"))

    def test_rejects_missing_type(self):
        self.assertTrue(validation_errors("Add engine endpoint"))

    def test_rejects_trailing_period(self):
        self.assertIn("ends with a period", validation_errors("fix: refuse an open frontend."))

    def test_rejects_long_subject(self):
        errors = validation_errors("feat: " + "a" * 80)
        self.assertTrue(any("exceeding" in e for e in errors))

    def test_main_exit_codes(self):
        self.assertEqual(main(["feat: add engine endpoint"]), 0)
        self.assertEqual(main(["bad subject"]), 1)
        self.assertEqual(main([]), 2)


if __name__ == "__main__":
    unittest.main()
