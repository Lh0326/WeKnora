"""Small, reproducible knowledge-tracing experiment; no WeKnora DB access.

Uses the pyBKT authors' Cognitive Tutor example for local research. Raw student
data stays in .local-data. This measures response prediction, not enterprise
reading comprehension, self-report reliability, or causal learning gains.
Requires numpy; source, exclusions, split, parameters and metrics are recorded.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import itertools
import json
import math
import platform
import urllib.request
from collections import Counter, defaultdict
from pathlib import Path

import numpy as np

VERSION = "bkt-small-data-v1"
DATA_COMMIT = "fc592907f41dfa0e71742ed1d24f1f6df08e1ad1"
DATA_URL = f"https://raw.githubusercontent.com/CAHLR/pyBKT-examples/{DATA_COMMIT}/data/ct.csv"
DEFAULT = np.array([0.2, 0.1, 0.2, 0.1])  # Explicit heuristic comparator, not fitted.
# Selected from sequence lengths before looking at predictive metrics: this
# public example has mostly 20-40 responses/user, so a 50-row prefix is unusable.
PREFIX_SIZES = (0, 3, 5, 10)
SUFFIX_START = 10
MIN_CALIBRATION_EVENTS = 20


def split_user(user):
    bucket = int(hashlib.sha256((VERSION + user).encode()).hexdigest()[:8], 16) % 10
    return "train" if bucket < 6 else "validation" if bucket < 8 else "test"


def load_rows(path):
    rows, exclusions, seen = [], Counter(), set()
    with path.open(encoding="utf-8-sig", newline="") as stream:
        for r in csv.DictReader(stream):
            user, skill = r["Anon Student Id"].strip(), r["KC(Default)"].strip()
            stamp = (r["First Transaction Time"] or r["Step Start Time"]).strip()
            label = r["Correct First Attempt"].strip()
            if not user or not skill or not stamp or label not in ("0", "1"):
                exclusions["missing_identity_skill_time_or_binary_label"] += 1
                continue
            # One row can list several skills. Do not silently duplicate its label.
            if "~~" in skill:
                exclusions["multi_skill_row"] += 1
                continue
            row_id = r["Row"].strip()
            identity = (user, row_id)
            if identity in seen:
                exclusions["duplicate_student_row"] += 1
                continue
            seen.add(identity)
            rows.append({"user": user, "skill": skill, "time": stamp,
                         "row": int(row_id), "y": int(label)})
    rows.sort(key=lambda r: (r["time"], r["row"]))
    return rows, dict(exclusions)


def sequences(rows):
    grouped = defaultdict(list)
    for r in rows:
        grouped[r["user"], r["skill"]].append(r["y"])
    return list(grouped.values())


def grid_fit(rows):
    """Exact finite candidate search, vectorized over candidates and sequences.

    No test or validation labels enter fitting. Guess + slip < 1 guarantees
    positive observations increase the pre-transition posterior. Learning after
    feedback is a distinct transition; it need not equal measured knowledge.
    """
    candidates = np.array(list(itertools.product(
        [0.05, 0.2, 0.4, 0.6, 0.8], [0.01, 0.03, 0.08, 0.15, 0.25],
        [0.05, 0.15, 0.25, 0.35], [0.05, 0.1, 0.2, 0.3])))
    seq = sequences(rows)
    if not seq:
        raise ValueError("empty training split")
    lengths = np.array([len(s) for s in seq])
    observations = np.zeros((len(seq), int(max(lengths))), dtype=np.int8)
    for i, s in enumerate(seq):
        observations[i, :len(s)] = s
    state = np.repeat(candidates[:, :1], len(seq), axis=1)
    learn, guess, slip = [candidates[:, j:j + 1] for j in (1, 2, 3)]
    losses = np.zeros(len(candidates))
    for t in range(observations.shape[1]):
        live = lengths > t
        m, y = np.clip(state[:, live], 1e-12, 1-1e-12), observations[live, t][None, :]
        q = np.clip(m * (1 - slip) + (1 - m) * guess, 1e-9, 1 - 1e-9)
        losses -= (y * np.log(q) + (1 - y) * np.log1p(-q)).sum(axis=1)
        posterior = np.where(y == 1, m * (1 - slip) / q, m * slip / (1 - q))
        posterior = np.clip(posterior, 1e-12, 1-1e-12)
        state[:, live] = np.clip(posterior + (1 - posterior) * learn, 1e-12, 1-1e-12)
    if not np.all(np.isfinite(losses)):
        raise ValueError("non-finite BKT likelihood; refuse to select a candidate")
    best = int(np.argmin(losses))
    return candidates[best], float(losses[best] / len(rows)), len(candidates)


def predict(rows, params):
    prior, learn, guess, slip = params
    state, output = {}, []
    for r in rows:
        key = (r["user"], r["skill"])
        m = float(np.clip(state.get(key, prior), 1e-12, 1-1e-12))
        q = float(m * (1 - slip) + (1 - m) * guess)
        output.append({**r, "p": q})  # Prediction precedes this row's outcome.
        posterior = m * (1 - slip) / q if r["y"] else m * slip / (1 - q)
        posterior = float(np.clip(posterior, 1e-12, 1-1e-12))
        state[key] = float(np.clip(posterior + (1 - posterior) * learn, 1e-12, 1-1e-12))
    return output


def metrics(rows):
    if not rows:
        return {"n": 0, "users": 0, "log_loss": None, "brier": None, "ece10": None}
    y = np.array([r["y"] for r in rows], float)
    p = np.clip(np.array([r["p"] for r in rows]), 1e-9, 1 - 1e-9)
    ece, bins = 0.0, []
    for i in range(10):
        mask = np.minimum((p * 10).astype(int), 9) == i
        if not mask.any():
            continue
        pred, actual = float(p[mask].mean()), float(y[mask].mean())
        ece += float(mask.mean()) * abs(pred - actual)
        bins.append({"bin": i, "n": int(mask.sum()), "predicted": pred, "observed": actual})
    per_user = defaultdict(list)
    for yy, pp, r in zip(y, p, rows):
        per_user[r["user"]].append(-yy * math.log(pp) - (1 - yy) * math.log1p(-pp))
    return {"n": len(rows), "users": len(per_user),
            "log_loss": float(-(y * np.log(p) + (1 - y) * np.log1p(-p)).mean()),
            "user_macro_log_loss": float(np.mean([np.mean(v) for v in per_user.values()])),
            "brier": float(((p - y) ** 2).mean()), "ece10": ece, "bins": bins}


def logistic(x):
    return 1 / (1 + np.exp(-np.clip(x, -30, 30)))


def fit_offset(prefix, strength):
    """One parameter, MAP with N(0, 1/strength) prior. Never fit on future rows."""
    if not prefix:
        return 0.0
    p = np.clip(np.array([r["p"] for r in prefix]), 1e-9, 1 - 1e-9)
    logits, y = np.log(p / (1 - p)), np.array([r["y"] for r in prefix])
    offset = 0.0
    for _ in range(20):
        q = logistic(logits + offset)
        step = ((q - y).sum() + strength * offset) / ((q * (1 - q)).sum() + strength)
        offset = float(np.clip(offset - step, -2, 2))
        if abs(step) < 1e-8:
            break
    return offset


def calibration_eval(predictions, prefix_size, strength):
    users, evaluated = defaultdict(list), []
    for r in predictions:
        users[r["user"]].append(r)
    for records in users.values():
        if len(records) < MIN_CALIBRATION_EVENTS:
            continue
        # Every prefix size uses identical future rows; calibration never sees them.
        offset = fit_offset(records[:prefix_size], strength)
        for r in records[SUFFIX_START:]:
            logit = math.log(r["p"] / (1 - r["p"]))
            evaluated.append({**r, "p": float(logistic(logit + offset))})
    return metrics(evaluated)


def run(path):
    rows, exclusions = load_rows(path)
    split = {k: [r for r in rows if split_user(r["user"]) == k]
             for k in ("train", "validation", "test")}
    train, validation, test = (split[k] for k in ("train", "validation", "test"))
    fitted, train_loss, candidates = grid_fit(train)
    skills = defaultdict(list)
    for r in train:
        skills[r["skill"]].append(r["y"])
    base = (sum(r["y"] for r in train) + 1) / (len(train) + 2)
    means = {s: (sum(ys) + 10 * base) / (len(ys) + 10) for s, ys in skills.items()}
    test_predictions = predict(test, fitted)
    validation_predictions = predict(validation, fitted)
    strengths = [2.0, 8.0, 32.0, 128.0]
    validation_scores = []
    for strength in strengths:
        runs = [calibration_eval(validation_predictions, n, strength) for n in PREFIX_SIZES[1:]]
        if not any(r["n"] for r in runs):
            raise ValueError("validation users have insufficient calibration history")
        score = np.mean([r["log_loss"] for r in runs if r["n"]])
        validation_scores.append((float(score), strength))
    strength = min(validation_scores)[1]
    models = {
        "training_global_mean": metrics([{**r, "p": base} for r in test]),
        "training_skill_mean": metrics([{**r, "p": means.get(r["skill"], base)} for r in test]),
        "heuristic_bkt": metrics(predict(test, DEFAULT)),
        "training_fitted_bkt": metrics(test_predictions),
    }
    return {
        "protocol": VERSION,
        "data": {"source": DATA_URL, "commit": DATA_COMMIT,
                 "sha256": hashlib.sha256(path.read_bytes()).hexdigest(),
                 "bytes": path.stat().st_size, "rows": len(rows), "exclusions": exclusions,
                 "raw_data_redistributed": False},
        "environment": {"python": platform.python_version(), "numpy": np.__version__},
        "split": {k: {"rows": len(v), "users": len({r['user'] for r in v}),
                      "skills": len({r['skill'] for r in v})} for k, v in split.items()},
        "parameters": {"order": ["L0", "T", "G", "S"], "heuristic": DEFAULT.tolist(),
                       "fitted": fitted.tolist(), "grid_candidates": candidates,
                       "training_log_loss": train_loss},
        "test_models": models,
        "calibration": {"selected_strength": strength,
                        "validation_selection": [{"strength": s, "mean_log_loss": v}
                                                 for v, s in validation_scores],
                        "same_test_suffix_after_first": SUFFIX_START,
                        "minimum_events_per_user": MIN_CALIBRATION_EVENTS,
                        "test": {str(n): calibration_eval(test_predictions, n, strength)
                                 for n in PREFIX_SIZES}},
        "limitations": [
            "Mathematics step correctness, not enterprise reading or self-report data.",
            "Public parameter transfer is not validated by fitting on this source domain.",
            "Calibration uses genuine previous answers, not subjective declarations.",
            "Micro metrics overweight active users; macro log loss is also reported.",
            "Predictive comparison is not evidence of causal learning-efficiency gains.",
        ],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--data", type=Path, default=Path(".local-data/research/learning-model/ct.csv"))
    parser.add_argument("--fetch", action="store_true", help="download authors' pinned public example locally")
    parser.add_argument("--out", type=Path, default=Path("docs/research/learning-model/public-bkt-results.json"))
    args = parser.parse_args()
    if args.fetch and not args.data.exists():
        args.data.parent.mkdir(parents=True, exist_ok=True)
        with urllib.request.urlopen(DATA_URL, timeout=45) as response:
            raw = response.read(10_000_001)
        if len(raw) > 10_000_000:
            raise ValueError("unexpected example file size")
        args.data.write_bytes(raw)
    result = run(args.data)
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False) + "\n", encoding="utf-8")
    print(json.dumps({"split": result["split"], "parameters": result["parameters"],
                      "test": {k: {s: v[s] for s in ("n", "users", "log_loss", "brier", "ece10")}
                               for k, v in result["test_models"].items()},
                      "calibration": {k: v["log_loss"] for k, v in result["calibration"]["test"].items()}}, indent=2))


if __name__ == "__main__":
    main()
