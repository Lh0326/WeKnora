"""Protocol checks: prediction causality, split isolation and calibration data use."""
import unittest

import numpy as np

import learning_model_benchmark as bench


def rows(labels, user="alice", skill="a"):
    return [{"user": user, "skill": skill, "y": y, "time": str(i), "row": i}
            for i, y in enumerate(labels)]


class ProtocolTest(unittest.TestCase):
    def test_current_label_never_enters_current_prediction(self):
        correct = bench.predict(rows([1, 1, 1]), bench.DEFAULT)
        wrong = bench.predict(rows([0, 1, 1]), bench.DEFAULT)
        self.assertEqual(correct[0]["p"], wrong[0]["p"])
        self.assertGreater(correct[1]["p"], wrong[1]["p"])

    def test_history_is_isolated_by_user_and_skill(self):
        history = rows([1] * 12)
        probes = rows([1], user="bob") + rows([1], skill="b")
        result = bench.predict(history + probes, bench.DEFAULT)
        cold = bench.predict(rows([1]), bench.DEFAULT)[0]["p"]
        self.assertEqual(result[-1]["p"], cold)
        self.assertEqual(result[-2]["p"], cold)

    def test_no_data_is_no_calibration(self):
        self.assertEqual(bench.fit_offset([], 8), 0)

    def test_small_sample_is_shrunk_toward_prior(self):
        prefix = [{"p": 0.5, "y": 1}] * 3
        weak = bench.fit_offset(prefix, 2)
        strong = bench.fit_offset(prefix, 128)
        self.assertGreater(weak, strong)
        self.assertGreater(strong, 0)

    def test_all_calibration_sizes_evaluate_identical_rows(self):
        predictions = bench.predict(rows(([1, 0, 1] * 15)), bench.DEFAULT)
        sizes = [bench.calibration_eval(predictions, n, 8)["n"] for n in bench.PREFIX_SIZES]
        self.assertEqual(sizes, [35] * len(bench.PREFIX_SIZES))

    def test_grid_likelihood_matches_sequential_prediction(self):
        training = rows([0, 0, 1, 1, 1]) + rows([1, 0, 1], user="bob")
        params, loss, _ = bench.grid_fit(training)
        self.assertAlmostEqual(loss, bench.metrics(bench.predict(training, params))["log_loss"])
        self.assertLess(params[2] + params[3], 1)
        self.assertTrue(np.all(params > 0))

    def test_long_sequences_remain_finite_and_grid_matches_scalar(self):
        training = rows([1] * 500 + [0] * 100 + [1, 0] * 50)
        with np.errstate(over="raise", invalid="raise", divide="raise"):
            params, loss, _ = bench.grid_fit(training)
            measured = bench.metrics(bench.predict(training, params))["log_loss"]
        self.assertTrue(np.isfinite(loss))
        self.assertAlmostEqual(loss, measured, places=8)


if __name__ == "__main__":
    unittest.main()
