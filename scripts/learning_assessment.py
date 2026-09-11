"""External holdout runner. Uses server-frozen states; never writes holdout answers to learning APIs.
Artifacts are append-only (exclusive create), hash linked, and timestamped at capture/presentation/grade.
Hashes detect later edits; they do not authenticate humans or prove an unobserved local clock.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
from datetime import datetime, timezone
from urllib.request import Request, urlopen
from urllib.parse import urlparse, quote

ORIGINS = ("engineering_fixture", "synthetic", "expert_scenario", "real_user")

def now():
    return datetime.now(timezone.utc).isoformat().replace("+00:00", "Z")

def digest(value):
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()

def load(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))

def save(path, value):
    # Never overwrite a freeze, presentation or result, even accidentally.
    with open(path, "x", encoding="utf-8") as stream:
        json.dump(value, stream, ensure_ascii=False, indent=2)
        stream.flush()
        os.fsync(stream.fileno())

def seal(record):
    return {"record": record, "sha256": digest(record)}

def unseal(path):
    artifact = load(path)
    if digest(artifact["record"]) != artifact["sha256"]:
        raise ValueError("artifact hash mismatch")
    return artifact["record"], artifact["sha256"]

def bank_items(bank):
    if not bank.get("frozen_rules") or not bank.get("items"):
        raise ValueError("bank requires frozen_rules and items")
    ids, families = set(), set()
    for item in bank["items"]:
        required = ("id", "objective_id", "objective_version", "family_id", "phase", "scenario", "assistance_mode", "reviewer", "rubric_version", "source_refs")
        if any(not item.get(key) for key in required) or item.get("status") != "published":
            raise ValueError("holdout item needs reviewed definition, source/version, condition and rubric")
        if item["id"] in ids or item["family_id"] in families:
            raise ValueError("held-out item IDs and families must be distinct across phases")
        if item["phase"] not in ("pretest", "posttest", "delayed") or item["assistance_mode"] not in ("closed_book", "open_book"):
            raise ValueError("invalid test phase/assistance mode")
        ids.add(item["id"]); families.add(item["family_id"])
        fields = item.get("fields", [])
        keys = item.get("answer_key", {})
        field_ids = [field.get("id") for field in fields]
        if not fields or len(set(field_ids)) != len(fields) or not keys or not set(keys) <= set(field_ids):
            raise ValueError("invalid fields or critical answer key")
        for field in fields:
            options = field.get("options", [])
            if not field.get("label") or len(options) < 2 or any(not isinstance(o, str) or not o for o in options) or len(set(options)) != len(options):
                raise ValueError("invalid task options")
            if field["id"] in keys and keys[field["id"]] not in options:
                raise ValueError("answer key outside field options")
    return {item["id"]: item for item in bank["items"]}

def capture(args):
    bank = load(args.bank); items = bank_items(bank)
    token = os.environ.get("WEKNORA_EVAL_TOKEN", "")
    if not token:
        raise ValueError("set WEKNORA_EVAL_TOKEN to this participant's authenticated bearer token")
    parsed = urlparse(args.base)
    if parsed.scheme != "https" and not (parsed.scheme == "http" and parsed.hostname in ("localhost", "127.0.0.1", "::1")):
        raise ValueError("use HTTPS except for local development")
    endpoint = args.base.rstrip("/") + "/api/v1/learning/kb/" + quote(args.kb, safe="") + "/assessment-freeze"
    request = Request(endpoint, headers={"Authorization": "Bearer " + token, "Accept": "application/json"})
    with urlopen(request, timeout=30) as response:
        payload = json.load(response)
    frozen = payload.get("data", payload)
    if frozen.get("knowledge_base_id") != args.kb or not frozen.get("state_as_of") or not frozen.get("snapshot_id"):
        raise ValueError("invalid freeze API response")
    for item in items.values():
        if frozen.get("objective_versions", {}).get(item["objective_id"]) != item["objective_version"]:
            raise ValueError("holdout objective version is not the currently frozen version")
        if item["family_id"] in frozen.get("evidence_families_before", []):
            raise ValueError("holdout family was already exposed to participant")
    save(args.out, seal({"kind": "capture", "participant_id": args.participant, "group": args.group,
                        "data_origin": args.origin, "frozen_rules": bank["frozen_rules"], "bank_sha256": digest(bank),
                        "captured_at": now(), "frozen": frozen, "assigned_items": [{"id": i["id"], "phase": i["phase"]} for i in items.values()]}))

def present(args):
    captured, capture_hash = unseal(args.capture)
    bank = load(args.bank); items = bank_items(bank)
    if digest(bank) != captured["bank_sha256"]:
        raise ValueError("holdout bank changed since freeze")
    item = items[args.item]
    # Only publish the learner-facing task, never the answer key or scoring descriptions.
    public = {key: item[key] for key in ("id", "scenario", "fields", "assistance_mode")}
    presented = now()
    if presented <= captured["frozen"]["state_as_of"]:
        raise ValueError("presentation clock precedes server freeze")
    record = {"kind": "presentation", "capture_sha256": capture_hash, "capture": captured,
              "item_id": args.item, "presented_at": presented, "public_task": public}
    save(args.out, seal(record))
    print(json.dumps(public, ensure_ascii=False, indent=2))

def grade(args):
    presentation, presentation_hash = unseal(args.presentation)
    captured = presentation["capture"]
    if digest(captured) != presentation["capture_sha256"]:
        raise ValueError("capture in presentation was edited")
    bank = load(args.bank); items = bank_items(bank)
    if digest(bank) != captured["bank_sha256"]:
        raise ValueError("rubric or holdout changed after freeze")
    item = items[presentation["item_id"]]; answers = load(args.answers)
    if not isinstance(answers, dict) or set(answers) != {f["id"] for f in item["fields"]}:
        raise ValueError("answers must contain exactly the presented field IDs")
    for field in item["fields"]:
        if not isinstance(answers[field["id"]], str) or answers[field["id"]] not in field["options"]:
            raise ValueError("submitted value outside options")
    checks = {key: answers[key] == value for key, value in item["answer_key"].items()}
    frozen = captured["frozen"]
    graded = now()
    if graded <= presentation["presented_at"]:
        raise ValueError("grading clock precedes presentation")
    snapshot = {"snapshot_id": frozen["snapshot_id"] + ":" + item["id"], "participant_id": captured["participant_id"],
                "objective_id": item["objective_id"], "state_before": frozen["goal_states_before"][item["objective_id"]],
                "evidence_families_before": frozen["evidence_families_before"], "policy_version": frozen["policy_version"],
                "state_as_of": frozen["state_as_of"], "item_id": item["id"], "family_id": item["family_id"],
                "phase": item["phase"], "presented_at": presentation["presented_at"], "graded_at": graded,
                "eligible": not args.assisted, "ineligible_reason": "assistant_helped" if args.assisted else "",
                "result": all(checks.values())}
    save(args.out, seal({"kind": "grade", "presentation_sha256": presentation_hash, "presentation": presentation,
                        "snapshot": snapshot, "checks": checks, "answers": answers, "scorer_version": "external-exact-critical-v1"}))

def assemble(args):
    captures = {}; origins = set(); rules = set(); participants = []; seen_grades = set()
    for path in args.captures:
        record, sha = unseal(path)
        pid = record["participant_id"]
        if pid in captures:
            raise ValueError("duplicate participant capture")
        captures[pid] = (record, sha); origins.add(record["data_origin"]); rules.add(record["frozen_rules"])
    if len(origins) != 1 or len(rules) != 1:
        raise ValueError("assess separate origins and frozen rules separately")
    by_user = {pid: [] for pid in captures}
    for path in args.grades:
        record, _ = unseal(path); presentation = record["presentation"]
        if digest(presentation) != record["presentation_sha256"]:
            raise ValueError("presentation in grade was edited")
        pid = record["snapshot"]["participant_id"]
        if pid not in captures or presentation["capture_sha256"] != captures[pid][1]:
            raise ValueError("grade has no matching frozen capture")
        sid = record["snapshot"]["snapshot_id"]
        if sid in seen_grades:
            raise ValueError("duplicate trial; retries cannot replace first result")
        seen_grades.add(sid); by_user[pid].append(record["snapshot"])
    for pid, (captured, sha) in sorted(captures.items()):
        snaps = by_user[pid]; post = [s for s in snaps if s["phase"] == "posttest" and s["eligible"]]
        assigned = [i for i in captured["assigned_items"] if i["phase"] == "posttest"]
        complete = bool(assigned) and {s["item_id"] for s in post} == {i["id"] for i in assigned}
        p = {"participant_id": pid, "group": captured["group"], "data_origin": captured["data_origin"],
             "goal_states_before": captured["frozen"]["goal_states_before"], "snapshots": snaps,
             "missing_post": not complete, "missing_reason": "" if complete else "missing_or_ineligible_assigned_posttest",
             "capture_sha256": sha}
        if complete:
            p.update(posttest_score=sum(s["result"] for s in post), posttest_max=len(assigned))
        participants.append(p)
    save(args.out, {"participants": participants, "frozen_rules": rules.pop(), "data_origin": origins.pop(), "random_seed": args.seed, "tuning_split": args.tuning})

def main():
    parser = argparse.ArgumentParser(description=__doc__); sub = parser.add_subparsers(dest="command", required=True)
    cap = sub.add_parser("capture"); cap.set_defaults(run=capture)
    for name in ("base", "kb", "participant", "bank", "out"): cap.add_argument("--" + name, required=True)
    cap.add_argument("--group", choices=("guided", "baseline"), required=True); cap.add_argument("--origin", choices=ORIGINS, required=True)
    pre = sub.add_parser("present"); pre.set_defaults(run=present)
    for name in ("capture", "bank", "item", "out"): pre.add_argument("--" + name, required=True)
    gra = sub.add_parser("grade"); gra.set_defaults(run=grade)
    for name in ("presentation", "bank", "answers", "out"): gra.add_argument("--" + name, required=True)
    gra.add_argument("--assisted", action="store_true")
    ass = sub.add_parser("assemble"); ass.set_defaults(run=assemble)
    ass.add_argument("--captures", nargs="+", required=True); ass.add_argument("--grades", nargs="*", default=[])
    ass.add_argument("--out", required=True); ass.add_argument("--seed", type=int, default=20260910); ass.add_argument("--tuning", action="store_true")
    args = parser.parse_args()
    try: args.run(args)
    except (ValueError, KeyError, OSError) as exc: parser.exit(1, str(exc) + "\n")
if __name__ == "__main__": main()
