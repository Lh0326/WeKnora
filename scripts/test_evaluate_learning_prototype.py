import json
from pathlib import Path
from tempfile import TemporaryDirectory
import unittest
import evaluate_learning_prototype as runner


class EvidenceReportTests(unittest.TestCase):
    def test_go_failures_skips_and_package_failures_survive(self):
        rows = [{"Action": "fail", "Package": "p", "Test": "TestBad"},
                {"Action": "skip", "Package": "p", "Test": "TestSkip"},
                {"Action": "fail", "Package": "p"}]
        result = runner.go_results("noise\n" + "\n".join(map(json.dumps, rows)))
        self.assertEqual(result, {"tests": {"p/TestBad": "fail", "p/TestSkip": "skip"},
                                  "packages": {"p": "fail"}})

    def test_empty_output_is_not_evidence_of_executed_tests(self):
        self.assertEqual(runner.observed_counts("OK", "assessment"), {})
        self.assertEqual(runner.observed_counts("npm test", "frontend"), {})
        self.assertEqual(runner.observed_counts("Ran 0 tests in 0.00s\nOK", "assessment"), {"tests": 0})
        for marker in ("#", "ℹ"):
            self.assertEqual(runner.observed_counts(f"{marker} tests 4\n{marker} fail 1\n{marker} cancelled 2\n", "frontend"),
                             {"tests": 4, "fail": 1, "cancelled": 2})

    def test_missing_partial_or_empty_planner_is_reported_as_failure(self):
        with TemporaryDirectory() as directory:
            path = Path(directory) / "planner.json"
            for value in (None, "{", "{}", '{"small_trials":[],"scale":[]}'):
                if value is not None:
                    path.write_text(value, encoding="utf-8")
                planner, error = runner.read_planner(path)
                self.assertIsNone(planner)
                self.assertTrue(error)


if __name__ == "__main__":
    unittest.main()
