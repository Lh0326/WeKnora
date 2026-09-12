import csv
import tempfile
import unittest
from pathlib import Path

import numpy as np

import learning_framework_benchmark as framework


class FrameworkProtocolTests(unittest.TestCase):
    def test_current_and_future_answers_do_not_enter_current_features(self):
        rows = [{"user": "u", "skill": "k", "y": y} for y in [1, 0, 1, 0]]
        before, _ = framework.history_features(rows)
        rows[2]["y"] = 0
        after, _ = framework.history_features(rows)
        np.testing.assert_equal(before[:3], after[:3])
        self.assertFalse(np.array_equal(before[3], after[3]))

    def test_history_does_not_transfer_across_students(self):
        rows = [{"user": "a", "skill": "k", "y": 1}, {"user": "b", "skill": "k", "y": 0}]
        x, cold = framework.history_features(rows)
        np.testing.assert_equal(x[:, 1:], 0)
        self.assertTrue(cold.all())

    def test_repeated_multiskill_events_are_excluded_as_one_event(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "data.csv"
            with path.open("w", newline="") as f:
                w = csv.DictWriter(f, fieldnames=["original", "user_id", "skill_id", "correct", "order_id"])
                w.writeheader()
                for order, skill in [(1, "a"), (1, "b"), (2, "a"), (2, "a")]:
                    w.writerow(dict(original="1", user_id="u", skill_id=skill, correct="1", order_id=order))
            rows, counts = framework.load_as(path)
            self.assertEqual(1, len(rows))
            self.assertEqual(2, rows[0]["row"])
            self.assertEqual(1, counts["multi_skill_or_conflicting_event"])
            self.assertEqual(1, counts["duplicate_rows_collapsed"])

    def test_newton_solution_is_stationary_and_transfer_ignores_skill_offsets(self):
        rng = np.random.default_rng(19)
        rows = [{"user": str(u), "skill": str(k % 3), "y": int(rng.random() < (.2 + .2*(k % 3)))}
                for u in range(20) for k in range(15)]
        model = framework.fit_history(rows, 1)
        x, _ = framework.history_features(rows)
        p = np.array([r["p"] for r in framework.history_predict(rows, model)])
        residual = p - np.array([r["y"] for r in rows])
        gradient = x.T @ residual + np.array([0, 1, 1, 1, 1]) * model["beta"]
        self.assertLess(np.max(np.abs(gradient)), 1e-5)
        base = framework.history_predict(rows, model, force_unknown=True)
        model["offsets"] += 50
        changed = framework.history_predict(rows, model, force_unknown=True)
        self.assertEqual([r["p"] for r in base], [r["p"] for r in changed])


if __name__ == "__main__":
    unittest.main()
