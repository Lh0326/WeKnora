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
COMPONENT_STATES = {"unseen": "未接触", "touched": "已接触", "learning": "已阅读",
                    "self_reported": "自评熟悉", "familiar": "检查支持", "review": "建议巩固"}

def target_type(bank):
    kind = bank.get("target_type", "objective")
    if kind not in ("objective", "component"):
        raise ValueError("target_type must be objective or component")
    return kind

def target_fields(captured):
    frozen = captured["frozen"]
    if captured.get("target_type", "objective") == "component":
        return (frozen["component_states_before"], frozen["component_versions"],
                frozen["component_evidence_families_before"], frozen["component_model_version"])
    return (frozen["goal_states_before"], frozen["objective_versions"],
            frozen["evidence_families_before"], frozen["policy_version"])

def seconds_between(start, end):
    values = [datetime.fromisoformat(value.replace("Z", "+00:00")) for value in (start, end)]
    if any(value.tzinfo is None for value in values):
        raise ValueError("timestamps require a timezone")
    return (values[1] - values[0]).total_seconds()

def journal_path(capture_path, item_id, kind):
    # A local append-only journal prevents changing --out to replace a first answer.
    # It is not a security boundary against someone editing/copying local artifacts.
    directory = Path(str(Path(capture_path).resolve()) + ".trials")
    directory.mkdir(exist_ok=True)
    return directory / (digest(item_id) + "." + kind + ".json")

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
    kind = target_type(bank)
    ids, families = set(), set()
    for item in bank["items"]:
        required = ("id", kind + "_id", kind + "_version", "family_id", "phase", "scenario", "assistance_mode", "reviewer", "rubric_version", "source_refs")
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
    kind = target_type(bank)
    requested = getattr(args, "target_type", None)
    if requested and requested != kind:
        raise ValueError("requested target type differs from the frozen bank")
    phase = getattr(args, "phase", None)
    if phase:
        items = {key: item for key, item in items.items() if item["phase"] == phase}
    if not items:
        raise ValueError("no items assigned for this phase")
    freeze_file = getattr(args, "freeze_file", None)
    if freeze_file:
        payload = load(freeze_file)
        freeze_source = "local_download_declared; source is not cryptographically authenticated"
    else:
        if not args.base:
            raise ValueError("provide --base or --freeze-file")
        token = os.environ.get("WEKNORA_EVAL_TOKEN", "")
        if not token:
            raise ValueError("set WEKNORA_EVAL_TOKEN or use a freeze downloaded through the signed-in product")
        parsed = urlparse(args.base)
        if parsed.scheme != "https" and not (parsed.scheme == "http" and parsed.hostname in ("localhost", "127.0.0.1", "::1")):
            raise ValueError("use HTTPS except for local development")
        endpoint = args.base.rstrip("/") + "/api/v1/learning/kb/" + quote(args.kb, safe="") + "/assessment-freeze"
        request = Request(endpoint, headers={"Authorization": "Bearer " + token, "Accept": "application/json"})
        with urlopen(request, timeout=30) as response:
            payload = json.load(response)
        freeze_source = "authenticated_api_request"
    frozen = payload.get("data", payload)
    if frozen.get("knowledge_base_id") != args.kb or not frozen.get("state_as_of") or not frozen.get("snapshot_id"):
        raise ValueError("invalid freeze API response")
    captured = {"kind": "capture", "participant_id": args.participant, "group": args.group,
                "target_type": kind, "block": getattr(args, "block", ""), "phase": phase,
                "material_origin": bank.get("material_origin", "unspecified"),
                "data_origin": args.origin, "frozen_rules": bank["frozen_rules"], "bank_sha256": digest(bank),
                "freeze_source": freeze_source, "freeze_payload_sha256": digest(payload),
                "captured_at": now(), "frozen": frozen,
                "assigned_items": [{"id": i["id"], "phase": i["phase"], "family_id": i["family_id"],
                                    "target_id": i[kind + "_id"]} for i in items.values()]}
    states, versions, families, _ = target_fields(captured)
    if kind == "component" and any(state not in COMPONENT_STATES for state in states.values()):
        raise ValueError("unknown component state; update the assessment runner")
    for item in items.values():
        target = item[kind + "_id"]
        if target not in states or versions.get(target) != item[kind + "_version"]:
            raise ValueError("holdout target version is not the currently frozen version")
        if kind == "component" and not frozen.get("component_available", {}).get(target, False):
            raise ValueError("component source is unavailable; review material before assessment")
        if item["family_id"] in families:
            raise ValueError("holdout family was already exposed to participant")
    save(args.out, seal(captured))

def present(args):
    captured, capture_hash = unseal(args.capture)
    bank = load(args.bank); items = bank_items(bank)
    if digest(bank) != captured["bank_sha256"]:
        raise ValueError("holdout bank changed since freeze")
    item = items[args.item]
    if args.item not in {item["id"] for item in captured["assigned_items"]}:
        raise ValueError("item was not assigned in this capture phase")
    # Only publish the learner-facing task, never the answer key or scoring descriptions.
    public = {key: item[key] for key in ("id", "scenario", "fields", "assistance_mode")}
    presented = now()
    if seconds_between(captured["frozen"]["state_as_of"], presented) < 0:
        raise ValueError("presentation clock precedes server freeze")
    canonical = journal_path(args.capture, args.item, "presentation")
    record = {"kind": "presentation", "capture_sha256": capture_hash, "capture": captured,
              "item_id": args.item, "presented_at": presented, "public_task": public,
              "journal_path": str(canonical)}
    try:
        save(canonical, seal(record))
    except FileExistsError:
        record, _ = unseal(canonical)
        if record["capture_sha256"] != capture_hash or record["public_task"] != public:
            raise ValueError("presentation journal conflicts with capture")
        # Reopening does not reset the first presentation time.
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
    submitted_at = None
    assisted = bool(args.assisted)
    if isinstance(answers, dict) and isinstance(answers.get("answers"), dict) and "submitted_at" in answers:
        if set(answers) - {"answers", "submitted_at", "assisted"} or not isinstance(answers.get("assisted", False), bool):
            raise ValueError("invalid answer receipt")
        submitted_at = answers["submitted_at"]
        assisted = assisted or answers.get("assisted", False)
        answers = answers["answers"]
    if not isinstance(answers, dict) or set(answers) != {f["id"] for f in item["fields"]}:
        raise ValueError("answers must contain exactly the presented field IDs")
    for field in item["fields"]:
        if not isinstance(answers[field["id"]], str) or answers[field["id"]] not in field["options"]:
            raise ValueError("submitted value outside options")
    checks = {key: answers[key] == value for key, value in item["answer_key"].items()}
    frozen = captured["frozen"]
    graded = now()
    response_end = submitted_at or graded
    response_seconds = seconds_between(presentation["presented_at"], response_end)
    if response_seconds <= 0 or seconds_between(response_end, graded) < 0:
        raise ValueError("grading clock precedes presentation")
    states, _, families, policy = target_fields(captured)
    kind = captured.get("target_type", "objective")
    snapshot = {"snapshot_id": frozen["snapshot_id"] + ":" + item["id"], "participant_id": captured["participant_id"],
                "target_type": kind, kind + "_id": item[kind + "_id"], "target_version": item[kind + "_version"],
                "state_before": states[item[kind + "_id"]],
                "evidence_families_before": families, "policy_version": policy,
                "state_as_of": frozen["state_as_of"], "item_id": item["id"], "family_id": item["family_id"],
                "phase": item["phase"], "presented_at": presentation["presented_at"], "graded_at": graded,
                "eligible": not assisted, "ineligible_reason": "assistant_helped" if assisted else "",
                "assistance_mode": item["assistance_mode"], "source_refs": item["source_refs"],
                "rubric_version": item["rubric_version"], "reviewer": item["reviewer"],
                "response_wall_seconds": response_seconds, "response_end_at": response_end,
                "timing_basis": "local_submission_receipt" if submitted_at else "grading_time_upper_bound",
                "result": all(checks.values())}
    record = {"kind": "grade", "presentation_sha256": presentation_hash, "presentation": presentation,
              "snapshot": snapshot, "checks": checks, "answers": answers, "scorer_version": "external-exact-critical-v2"}
    canonical = Path(presentation.get("journal_path", str(Path(args.presentation).resolve())))
    if presentation.get("journal_path"):
        _, canonical_hash = unseal(canonical)
        if canonical_hash != presentation_hash:
            raise ValueError("presentation differs from its first-presentation journal")
    first_answer = canonical.with_name(canonical.name + ".first-answer.json")
    try:
        save(first_answer, seal(record))
    except FileExistsError:
        first, _ = unseal(first_answer)
        if first["answers"] != answers or first["snapshot"]["eligible"] != snapshot["eligible"]:
            raise ValueError("first answer is already recorded; retries cannot replace it")
        record = first
    save(args.out, seal(record))

def assessment_records(args):
    captures = {}; origins = set(); rules = set(); kinds = set(); seen_grades = set()
    for path in args.captures:
        record, sha = unseal(path)
        if sha in captures or record.get("kind") != "capture" or not record.get("participant_id"):
            raise ValueError("invalid or duplicate capture")
        if record["data_origin"] not in ORIGINS or record["group"] not in ("guided", "baseline"):
            raise ValueError("invalid capture origin/group")
        captures[sha] = record; origins.add(record["data_origin"]); rules.add(record["frozen_rules"])
        kinds.add(record.get("target_type", "objective"))
    if len(origins) != 1 or len(rules) != 1 or len(kinds) != 1 or not kinds <= {"objective", "component"}:
        raise ValueError("assess separate origins, target types and frozen rules separately")
    by_capture = {sha: [] for sha in captures}
    for path in args.grades:
        record, _ = unseal(path); presentation = record["presentation"]
        if digest(presentation) != record["presentation_sha256"]:
            raise ValueError("presentation in grade was edited")
        sha = presentation["capture_sha256"]
        if sha not in captures or digest(presentation["capture"]) != sha:
            raise ValueError("grade has no matching frozen capture")
        captured = captures[sha]; snapshot = record["snapshot"]
        kind = captured.get("target_type", "objective")
        assigned = {item["id"]: item for item in captured["assigned_items"]}
        item = assigned.get(snapshot["item_id"])
        states, versions, families, policy = target_fields(captured)
        target = snapshot.get(kind + "_id")
        if (not item or snapshot["participant_id"] != captured["participant_id"] or
                snapshot["phase"] != item["phase"] or target not in states or
                snapshot.get("target_type", "objective") != kind or
                ("target_version" in snapshot and snapshot["target_version"] != versions[target]) or
                snapshot["snapshot_id"] != captured["frozen"]["snapshot_id"] + ":" + snapshot["item_id"] or
                snapshot["state_before"] != states[target] or snapshot["policy_version"] != policy or
                snapshot["evidence_families_before"] != families or
                snapshot["state_as_of"] != captured["frozen"]["state_as_of"] or
                snapshot["presented_at"] != presentation["presented_at"] or
                snapshot["item_id"] != presentation["item_id"] or
                (item.get("family_id") and snapshot["family_id"] != item["family_id"]) or
                (item.get("target_id") and target != item["target_id"]) or
                not isinstance(snapshot["result"], bool) or not isinstance(snapshot["eligible"], bool) or
                not record.get("checks") or any(not isinstance(value, bool) for value in record["checks"].values()) or
                snapshot["result"] != all(record["checks"].values())):
            raise ValueError("grade does not match the frozen assignment or deterministic checks")
        if seconds_between(snapshot["state_as_of"], snapshot["presented_at"]) < 0 or seconds_between(snapshot["presented_at"], snapshot["graded_at"]) <= 0:
            raise ValueError("invalid assessment timestamp order")
        if "response_end_at" in snapshot and (seconds_between(snapshot["presented_at"], snapshot["response_end_at"]) <= 0 or
                                               seconds_between(snapshot["response_end_at"], snapshot["graded_at"]) < 0):
            raise ValueError("answer receipt timestamp outside presentation/grading interval")
        sid = snapshot["snapshot_id"]
        if sid in seen_grades:
            raise ValueError("duplicate trial; retries cannot replace first result")
        seen_grades.add(sid); by_capture[sha].append(snapshot)
    return captures, by_capture, kinds.pop(), origins.pop(), rules.pop()

def assemble(args):
    captures, by_capture, kind, origin, rules = assessment_records(args)
    participants = []; seen_users = set()
    for sha, captured in sorted(captures.items()):
        pid = captured["participant_id"]
        if pid in seen_users:
            raise ValueError("duplicate participant capture; use describe for one person's multiple phases/blocks")
        seen_users.add(pid)
        snaps = by_capture[sha]; post = [s for s in snaps if s["phase"] == "posttest" and s["eligible"]]
        assigned = [i for i in captured["assigned_items"] if i["phase"] == "posttest"]
        complete = bool(assigned) and {s["item_id"] for s in post} == {i["id"] for i in assigned}
        p = {"participant_id": pid, "group": captured["group"], "data_origin": captured["data_origin"],
             "target_type": kind, "goal_states_before": target_fields(captured)[0], "snapshots": snaps,
             "missing_post": not complete, "missing_reason": "" if complete else "missing_or_ineligible_assigned_posttest",
             "capture_sha256": sha}
        if complete:
            p.update(posttest_score=sum(s["result"] for s in post), posttest_max=len(assigned))
        participants.append(p)
    save(args.out, {"participants": participants, "target_type": kind, "frozen_rules": rules,
                    "data_origin": origin, "random_seed": args.seed, "tuning_split": args.tuning})

def describe(args):
    """Describe one real or explicitly labelled fixture participant, without population inference."""
    captures, by_capture, kind, origin, rules = assessment_records(args)
    participants = {captured["participant_id"] for captured in captures.values()}
    if len(participants) != 1:
        raise ValueError("describe requires exactly one participant; do not relabel blocks as different people")
    observations = []
    for sha, captured in captures.items():
        for snapshot in by_capture[sha]:
            observations.append((snapshot, sha))
    observations.sort(key=lambda entry: (datetime.fromisoformat(entry[0]["presented_at"].replace("Z", "+00:00")), entry[0]["snapshot_id"]))
    excluded = {}; seen_families = set(); eligible = {}
    for snapshot, sha in observations:
        family = snapshot["family_id"]
        reason = ""
        if family in seen_families or family in snapshot["evidence_families_before"]:
            reason = "family_overlap"
        elif not snapshot["eligible"]:
            reason = snapshot.get("ineligible_reason") or "ineligible"
        seen_families.add(family)  # An assisted/failed answer still exposes the family.
        if reason:
            excluded[snapshot["snapshot_id"]] = reason
        else:
            eligible[snapshot["snapshot_id"]] = snapshot
    blocks = []; states = {}
    for sha, captured in sorted(captures.items(), key=lambda entry: (entry[1]["frozen"]["state_as_of"], entry[0])):
        snapshots = by_capture[sha]; assigned = captured["assigned_items"]
        for phase in sorted({item["phase"] for item in assigned}):
            assigned_ids = {item["id"] for item in assigned if item["phase"] == phase}
            submitted = [s for s in snapshots if s["phase"] == phase]
            valid = [s for s in submitted if s["snapshot_id"] in eligible]
            missing = sorted(assigned_ids - {s["item_id"] for s in submitted})
            complete = len(valid) == len(assigned_ids)
            passes = sum(s["result"] for s in valid)
            response_start = min((s["presented_at"] for s in submitted), key=lambda value: datetime.fromisoformat(value.replace("Z", "+00:00")), default=None)
            response_end = max((s.get("response_end_at", s["graded_at"]) for s in submitted), key=lambda value: datetime.fromisoformat(value.replace("Z", "+00:00")), default=None)
            blocks.append({"capture_sha256": sha, "snapshot_id": captured["frozen"]["snapshot_id"],
                           "block": captured.get("block", ""), "group": captured["group"], "phase": phase,
                           "freeze_source": captured.get("freeze_source", "legacy_capture_source_unspecified"),
                           "assigned": len(assigned_ids), "submitted": len(submitted), "eligible": len(valid),
                           "passes": passes, "complete": complete, "missing_item_ids": missing,
                           "missing_reasons": {item_id: "no_submission" for item_id in missing},
                           "score_percent": 100 * passes / len(assigned_ids) if complete else None,
                           "response_wall_seconds": seconds_between(response_start, response_end) if submitted else None,
                           "timing_definition": "first_submitted_item_presentation_to_last_submission; overlapping item windows are not added",
                           "frozen_states": target_fields(captured)[0],
                           "exposure_complete": captured["frozen"].get("component_exposure_complete", False) if kind == "component" else False})
            if phase == "posttest":
                for snapshot in valid:
                    state = snapshot["state_before"]
                    row = states.setdefault(state, {"label": COMPONENT_STATES.get(state, state) if kind == "component" else state,
                                                     "trials": 0, "passes": 0})
                    row["trials"] += 1; row["passes"] += int(snapshot["result"])
    for row in states.values():
        row["failures"] = row["trials"] - row["passes"]
        row["pass_rate"] = row["passes"] / row["trials"]
    supported = states.get("familiar", {}) if kind == "component" else {}
    save(args.out, {"report_type": "single-participant-descriptive-v1", "target_type": kind,
                    "participant_id": participants.pop(), "participant_count": 1, "data_origin": origin,
                    "material_origins": sorted({captured.get("material_origin", "unspecified") for captured in captures.values()}),
                    "frozen_rules": rules, "blocks": blocks, "posttest_by_state": states,
                    "check_supported_posttest_trials": supported.get("trials", 0) if kind == "component" else None,
                    "check_supported_posttest_failures": supported.get("failures", 0) if kind == "component" else None,
                    "check_supported_posttest_failure_rate": 1 - supported["pass_rate"] if supported else None,
                    "exclusions": excluded, "observations": [entry[0] for entry in observations],
                    "learning_time_seconds": None, "population_effect": None,
                    "limitations": ["One participant and multiple blocks are not multiple independent users; no population effect or significance is estimated.",
                                    "Response wall time includes file entry, interruptions and grading delay; it is not active learning time.",
                                    "Phase scores use different items; no pre/post gain or causal effect is computed without an independently justified comparable scale and allocation.",
                                    "Family IDs cannot detect paraphrased equivalents, displayed-but-unsubmitted tasks or offline exposure; review these separately.",
                                    "Origin/reviewer labels and local timestamps are declarations, not authenticated evidence of participation or independent review."]})

def main():
    parser = argparse.ArgumentParser(description=__doc__); sub = parser.add_subparsers(dest="command", required=True)
    cap = sub.add_parser("capture"); cap.set_defaults(run=capture)
    for name in ("kb", "participant", "bank", "out"): cap.add_argument("--" + name, required=True)
    source = cap.add_mutually_exclusive_group(required=True)
    source.add_argument("--base"); source.add_argument("--freeze-file", help="Unmodified raw assessment snapshot downloaded through the signed-in product")
    cap.add_argument("--group", choices=("guided", "baseline"), required=True); cap.add_argument("--origin", choices=ORIGINS, required=True)
    cap.add_argument("--target-type", choices=("objective", "component")); cap.add_argument("--phase", choices=("pretest", "posttest", "delayed"))
    cap.add_argument("--block", default="", help="Frozen block label for an individual descriptive trial")
    pre = sub.add_parser("present"); pre.set_defaults(run=present)
    for name in ("capture", "bank", "item", "out"): pre.add_argument("--" + name, required=True)
    gra = sub.add_parser("grade"); gra.set_defaults(run=grade)
    for name in ("presentation", "bank", "answers", "out"): gra.add_argument("--" + name, required=True)
    gra.add_argument("--assisted", action="store_true")
    ass = sub.add_parser("assemble"); ass.set_defaults(run=assemble)
    ass.add_argument("--captures", nargs="+", required=True); ass.add_argument("--grades", nargs="*", default=[])
    ass.add_argument("--out", required=True); ass.add_argument("--seed", type=int, default=20260910); ass.add_argument("--tuning", action="store_true")
    des = sub.add_parser("describe"); des.set_defaults(run=describe)
    des.add_argument("--captures", nargs="+", required=True); des.add_argument("--grades", nargs="*", default=[])
    des.add_argument("--out", required=True)
    args = parser.parse_args()
    try: args.run(args)
    except (ValueError, KeyError, OSError) as exc: parser.exit(1, str(exc) + "\n")
if __name__ == "__main__": main()
