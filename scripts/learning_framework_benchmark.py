"""Frozen, two-corpus response-prediction comparison. NumPy only; no DB access.

Protocol and schema audit live in docs/research/learning-framework. Raw records
stay local. Fits regularized history logistic regression by block Newton steps,
exploiting the diagonal skill-intercept Hessian rather than adding an ML stack.
"""
from __future__ import annotations

import argparse
import csv
import hashlib
import json
import platform
import time
from collections import Counter, defaultdict
from pathlib import Path

import numpy as np

import learning_model_benchmark as bkt

ROOT = Path(__file__).resolve().parents[1]
PROTOCOL = ROOT / "docs/research/learning-framework/evaluation-protocol.json"
SCHEMA = ROOT / "docs/research/learning-framework/schema-audit.md"
SALT = "learning-framework-prediction-v1"
SEED = 20260912


def split_user(user):
    bucket = int(hashlib.sha256((SALT + user).encode()).hexdigest()[:8], 16) % 10
    return "train" if bucket < 6 else "validation" if bucket < 8 else "test"


def load_as(path):
    events, exclusions = {}, Counter()
    raw_count = 0
    with path.open(encoding="latin-1", newline="") as stream:
        for r in csv.DictReader(stream):
            raw_count += 1
            if r["original"] != "1":
                exclusions["scaffolding_row"] += 1
                continue
            user, skill, label = r["user_id"].strip(), r["skill_id"].strip(), r["correct"].strip()
            if not user or not skill or label not in ("0", "1"):
                exclusions["missing_identity_skill_or_binary_label_row"] += 1
                continue
            key = (user, int(r["order_id"]))
            if key not in events:
                events[key] = {"skills": set(), "labels": set(), "rows": 0}
            e = events[key]
            e["skills"].add(skill)
            e["labels"].add(int(label))
            e["rows"] += 1
    out = []
    for (user, order), e in events.items():
        if len(e["skills"]) != 1 or len(e["labels"]) != 1:
            exclusions["multi_skill_or_conflicting_event"] += 1
            exclusions["rows_in_excluded_events"] += e["rows"]
            continue
        exclusions["duplicate_rows_collapsed"] += e["rows"] - 1
        out.append({"user": user, "skill": next(iter(e["skills"])), "row": order,
                    "time": order, "y": next(iter(e["labels"]))})
    out.sort(key=lambda r: (r["row"], r["user"]))
    return out, {"raw_rows": raw_count, **dict(exclusions)}


def history_features(rows):
    """No IDs or current answers as regressors; reset histories per call/split."""
    user_history, skill_history = defaultdict(lambda: [0, 0]), defaultdict(lambda: [0, 0])
    x = np.ones((len(rows), 5), dtype=float)
    cold = np.zeros(len(rows), dtype=bool)
    for i, r in enumerate(rows):
        u, k = r["user"], (r["user"], r["skill"])
        us, uf = user_history[u]
        ss, sf = skill_history[k]
        cold[i] = us + uf == 0
        x[i, 1:] = np.log1p([ss, sf, us, uf])
        # Observe only after the current feature vector has been frozen.
        user_history[u][0 if r["y"] else 1] += 1
        skill_history[k][0 if r["y"] else 1] += 1
    return x, cold


def fit_history(rows, ridge):
    skills = {s: i for i, s in enumerate(sorted({r["skill"] for r in rows}))}
    ids = np.array([skills[r["skill"]] for r in rows], dtype=int)
    x, _ = history_features(rows)
    y = np.array([r["y"] for r in rows], dtype=float)
    base = (y.sum() + 1) / (len(y) + 2)
    beta, offsets = np.zeros(5), np.zeros(len(skills))
    beta[0] = np.log(base / (1 - base))
    penalty = np.array([0, ridge, ridge, ridge, ridge])

    def objective(b, d):
        z = x @ b + d[ids]
        return float(np.sum(np.logaddexp(0, z) - y * z)
                     + .5 * np.dot(penalty * b, b) + .5 * ridge * np.dot(d, d))

    converged = False
    for iteration in range(80):
        z = x @ beta + offsets[ids]
        p = bkt.logistic(z)
        w, error = p * (1 - p), p - y
        gb = x.T @ error + penalty * beta
        gd = np.bincount(ids, weights=error, minlength=len(skills)) + ridge * offsets
        if max(np.max(np.abs(gb)), np.max(np.abs(gd))) < 1e-5:
            converged = True
            break
        h = x.T @ (w[:, None] * x) + np.diag(penalty)
        diagonal = np.bincount(ids, weights=w, minlength=len(skills)) + ridge
        cross = np.array([np.bincount(ids, weights=w*x[:, j], minlength=len(skills)) for j in range(5)])
        step_beta = np.linalg.solve(h - (cross / diagonal) @ cross.T,
                                   gb - cross @ (gd / diagonal))
        step_offsets = (gd - cross.T @ step_beta) / diagonal
        before, scale = objective(beta, offsets), 1.0
        slope = np.dot(gb, step_beta) + np.dot(gd, step_offsets)
        while scale > 1e-8:
            nb, nd = beta - scale * step_beta, offsets - scale * step_offsets
            if objective(nb, nd) <= before - 1e-4 * scale * slope:
                beta, offsets = nb, nd
                break
            scale *= .5
        else:
            raise RuntimeError("history Newton line search failed")
    if not converged:
        raise RuntimeError("history model did not converge; do not publish partial fit")
    return {"skills": skills, "beta": beta, "offsets": offsets, "ridge": ridge,
            "iterations": iteration + 1, "objective": objective(beta, offsets)}


def history_predict(rows, model, force_unknown=False):
    x, _ = history_features(rows)
    offsets = np.array([0.0 if force_unknown or r["skill"] not in model["skills"]
                        else model["offsets"][model["skills"][r["skill"]]] for r in rows])
    p = bkt.logistic(x @ model["beta"] + offsets)
    return [{**r, "p": float(q)} for r, q in zip(rows, p)]


def user_losses(predictions):
    out = defaultdict(list)
    for r in predictions:
        p = float(np.clip(r["p"], 1e-9, 1-1e-9))
        out[r["user"]].append(-np.log(p if r["y"] else 1-p))
    return {u: float(np.mean(v)) for u, v in out.items()}


def paired_bootstrap(a, b):
    aa, bb = user_losses(a), user_losses(b)
    if aa.keys() != bb.keys():
        raise ValueError("paired evaluation must use identical learners")
    diff = np.array([aa[u]-bb[u] for u in sorted(aa)])
    rng = np.random.default_rng(SEED)
    samples = [float(np.mean(diff[rng.integers(len(diff), size=len(diff))])) for _ in range(1000)]
    return {"unit": "student", "users": len(diff), "difference": float(np.mean(diff)),
            "ci95": np.quantile(samples, [.025, .975]).tolist(), "seed": SEED, "replicates": 1000}


def corpus_run(name, path):
    print(f"{name}: loading and auditing events", flush=True)
    start = time.monotonic()
    rows, excluded = load_as(path) if name == "as" else bkt.load_rows(path)
    split = {k: [r for r in rows if split_user(r["user"]) == k] for k in ("train", "validation", "test")}
    train, validation, test = (split[k] for k in ("train", "validation", "test"))
    assert all(split.values())
    print(f"{name}: {len(rows)} eligible events; fitting BKT training grid", flush=True)
    params, train_loss, count = bkt.grid_fit(train)
    base = (sum(r["y"] for r in train) + 1)/(len(train)+2)
    labels = defaultdict(list)
    for r in train:
        labels[r["skill"]].append(r["y"])
    means = {k: (sum(v)+10*base)/(len(v)+10) for k, v in labels.items()}
    fits, validation_scores = {}, {}
    for ridge in (.1, 1, 10, 100):
        print(f"{name}: fitting history regression, ridge={ridge}", flush=True)
        fits[ridge] = fit_history(train, ridge)
        validation_scores[ridge] = bkt.metrics(history_predict(validation, fits[ridge]))["log_loss"]
    chosen_ridge = min(validation_scores, key=lambda k: (validation_scores[k], k))
    model = fits[chosen_ridge]

    def predict_all(records):
        return {
            "global_mean": [{**r, "p": base} for r in records],
            "skill_mean": [{**r, "p": means.get(r["skill"], base)} for r in records],
            "bkt_default": bkt.predict(records, bkt.DEFAULT),
            "bkt_fitted": bkt.predict(records, params),
            "history_logistic": history_predict(records, model),
        }

    vp = predict_all(validation)
    vm = {k: bkt.metrics(v) for k, v in vp.items()}
    selected = min(vm, key=lambda k: (vm[k]["log_loss"], k))
    # Everything above this line is frozen before test prediction is inspected.
    predictions = predict_all(test)
    _, cold = history_features(test)
    masks = {"first_student_event": cold,
             "seen_training_skill": np.array([r["skill"] in means for r in test]),
             "unseen_training_skill": np.array([r["skill"] not in means for r in test])}
    groups = {g: {k: bkt.metrics([r for r, keep in zip(v, mask) if keep])
                  for k, v in predictions.items()} for g, mask in masks.items()}
    shuffled = [dict(r) for r in train]
    ys = np.array([r["y"] for r in shuffled])
    np.random.default_rng(SEED).shuffle(ys)
    for r, y in zip(shuffled, ys):
        r["y"] = int(y)
    print(f"{name}: negative control and paired uncertainty", flush=True)
    negative = fit_history(shuffled, chosen_ridge)
    result = {
        "data": {"name": name, "bytes": path.stat().st_size,
                 "sha256": hashlib.sha256(path.read_bytes()).hexdigest(), "exclusions": excluded},
        "split": {k: {"events": len(v), "users": len({r['user'] for r in v}),
                      "skills": len({r['skill'] for r in v})} for k, v in split.items()},
        "fit": {"bkt": params.tolist(), "bkt_training_log_loss": train_loss, "bkt_grid_size": count,
                "history_ridge": chosen_ridge, "history_validation_by_ridge": validation_scores,
                "history_shared_beta": model["beta"].tolist(), "history_newton_iterations": model["iterations"]},
        "validation": vm, "selected_on_validation": selected,
        "test": {k: bkt.metrics(v) for k, v in predictions.items()}, "test_groups": groups,
        "paired_selected_minus_skill_mean": paired_bootstrap(predictions[selected], predictions["skill_mean"]),
        "shuffled_training_negative_control": bkt.metrics(history_predict(test, negative)),
        "elapsed_seconds": time.monotonic() - start,
    }
    return result, {"test": test, "history": model, "bkt": params, "global_mean": base}


def run(paths, output):
    protocol = json.loads(PROTOCOL.read_text(encoding="utf-8"))
    if protocol["version"] != SALT:
        raise ValueError("code/protocol version mismatch")
    result = {"protocol": protocol, "protocol_sha256": hashlib.sha256(PROTOCOL.read_bytes()).hexdigest(),
              "schema_sha256": hashlib.sha256(SCHEMA.read_bytes()).hexdigest(),
              "script_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
              "bkt_implementation_sha256": hashlib.sha256(Path(bkt.__file__).read_bytes()).hexdigest(),
              "environment": {"python": platform.python_version(), "numpy": np.__version__}, "corpora": {}}
    models = {}
    for name, path in paths.items():
        result["corpora"][name], models[name] = corpus_run(name, path)
        # Separate progress artifact; never mistake it for the final result.
        output.with_suffix(".partial.json").write_text(json.dumps(result, indent=2, allow_nan=False), encoding="utf-8")
    result["cross_corpus_stress"] = {}
    for source, target in (("ct", "as"), ("as", "ct")):
        m, rows = models[source], models[target]["test"]
        result["cross_corpus_stress"][f"{source}_to_{target}"] = {
            "source_global_mean": bkt.metrics([{**r, "p": m["global_mean"]} for r in rows]),
            "source_bkt": bkt.metrics(bkt.predict(rows, m["bkt"])),
            "source_shared_history_unknown_skills": bkt.metrics(history_predict(rows, m["history"], force_unknown=True)),
            "target_training_skill_mean_reference": result["corpora"][target]["test"]["skill_mean"],
        }
    result["limitations"] = [
        "CT test has been examined in earlier work; new analysis is exploratory.",
        "AS label 0 combines first failure and help; not equivalent to independent local checks.",
        "Single-skill, main-problem subset; not all student activity or all component types.",
        "Test measures new users on these corpora; unknown-skill groups may be empty and are reported as such.",
        "Skill intercepts always unknown in transfer stress; no cross-domain parameter refitting.",
        "Matched distribution prediction is not causal policy improvement or enterprise reading validation.",
        "No public parameters are deployed to WeKnora component mastery.",
    ]
    output.write_text(json.dumps(result, indent=2, ensure_ascii=False, allow_nan=False), encoding="utf-8")
    print(json.dumps({n: {"selected": r["selected_on_validation"], "test_log_loss": {m: v["log_loss"] for m, v in r["test"].items()}}
                      for n, r in result["corpora"].items()}, indent=2), flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ct", type=Path, required=True)
    parser.add_argument("--as-data", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    run({"ct": args.ct, "as": args.as_data}, args.output)
