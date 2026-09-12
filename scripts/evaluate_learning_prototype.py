"""Run production-linked checks; preserve evidence without inventing efficacy scores."""
import argparse
import hashlib
import json
import math
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import time
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[1]
PROTOCOL = Path("docs/evaluation/learning-prototype/README.md")
PACKAGES = [
    "./internal/application/service/learning/...", "./internal/application/repository",
    "./internal/handler", "./internal/router", "./internal/types/...",
    "./internal/database", "./cmd/learning-bench", "./cmd/learning-component-import",
]
AXES = {
    "关联": ["TestComponentGranularitySeparatesSamePageEvidence",
           "TestComponentMultiSourceStableIdentityAndContextBoundary",
           "TestComponentPackRejectsWrongSourcesAndCycles",
           "TestComponentMemoryFanoutMassDoesNotGrowWithCatalogueSize",
           "TestComponentMemoryRejectsWrongScopeStaleAndNonFiniteMappings"],
    "度量": ["TestComponentPassiveSignalsDoNotManufacturePerformance",
           "TestComponentVersionsAndFutureEvidenceDoNotLeak",
           "TestComponentDuplicateFamilyAndReportsCannotAccumulate",
           "TestComponentSupportNeedsDifferentIndependentFamilies",
           "TestComponentRecallUsesOnlyExplicitRecallAndBlocksRapidRepeat"],
    "引导": ["TestComponentJointPlanAvoidsShortNodeTrap",
           "TestComponentDifficultReportCanProgressAfterReadingWithoutKnownDeclaration",
           "TestComponentPlanCheckBudgetAndInvalidSources",
           "TestComponentGoalBudgetPartialIsDisclosed",
           "TestComponentPlannerOfflineEvaluation"],
}


def now():
    return datetime.now(timezone.utc).isoformat()


def source_hashes():
    # Hash only build/evaluation inputs, never .env, runtime data or learner records.
    paths = {PROTOCOL, Path("go.mod"), Path("go.sum"), Path("frontend/package.json"),
             Path("frontend/package-lock.json")}
    paths.update(p.relative_to(ROOT) for p in (ROOT / "frontend").glob("*.json"))
    paths.update(p.relative_to(ROOT) for p in (ROOT / "frontend").glob("*config.*"))
    paths.update(p.relative_to(ROOT) for p in (ROOT / "frontend/src").rglob("*") if p.is_file())
    # Older checkouts still read these fixtures outside testdata.
    for folder in ("docs/research/learning-framework", "docs/research/learning-model", "docs/evaluation"):
        paths.update(p.relative_to(ROOT) for p in (ROOT / folder).rglob("*")
                     if p.is_file() and p.suffix in (".json", ".md"))
    for folder in ("internal", "migrations", "config", "cmd/learning-bench",
                   "cmd/learning-component-import", "frontend/src", "scripts"):
        paths.update(p.relative_to(ROOT) for p in (ROOT / folder).rglob("*")
                     if p.is_file() and p.suffix in (".go", ".py", ".json", ".sql", ".md",
                                                   ".ts", ".vue", ".mjs", ".css", ".less", ".yaml", ".yml"))
    return {p.as_posix(): hashlib.sha256((ROOT / p).read_bytes()).hexdigest()
            for p in sorted(paths) if (ROOT / p).is_file()}


def go_results(log):
    tests, packages = {}, {}
    for line in log.splitlines():
        try:
            row = json.loads(line)
        except json.JSONDecodeError:
            continue
        if not isinstance(row, dict) or row.get("Action") not in ("pass", "fail", "skip"):
            continue
        key = row.get("Package", "")
        if row.get("Test"):
            tests[key + "/" + row["Test"]] = row["Action"]
        else:
            packages[key] = row["Action"]
    return {"tests": tests, "packages": packages}


def observed_counts(log, kind):
    if kind == "assessment":
        found = re.search(r"Ran (\d+) tests? in", log)
        return {"tests": int(found[1])} if found else {}
    counts = {}
    for name in ("tests", "fail", "cancelled", "skipped", "todo"):
        found = re.search(r"(?m)^\s*(?:#|ℹ)?\s*" + name + r"\s+(\d+)\s*$", log)
        if found:
            counts[name] = int(found[1])
    return counts


def read_planner(path):
    try:
        result = json.loads(path.read_text(encoding="utf-8"))
        trials, scale = result["small_trials"], result["scale"]
        if len(trials) != 100 or [row["nodes"] for row in scale] != [20, 40, 100, 500]:
            raise ValueError("expected 100 small trials and all four scale cases")
        for row in trials:
            for key in ("oracle", "search", "greedy"):
                if not isinstance(row[key], int):
                    raise ValueError("planner utility must be an integer")
        for row in scale:
            if not isinstance(row["greedy"], int) or not math.isfinite(row["seconds"]):
                raise ValueError("invalid scale baseline or duration")
            for key in ("utility", "upper_bound"):
                if not isinstance(row["plan"][key], int):
                    raise ValueError("missing scale utility/bound")
        return result, None
    except (OSError, ValueError, KeyError, TypeError) as exc:
        return None, str(exc)


def run_check(name, args, cwd, out, env):
    started = time.monotonic()
    executable = shutil.which(args[0])
    stdout, stderr = out / (name + ".stdout.log"), out / (name + ".stderr.log")
    with stdout.open("w", encoding="utf-8") as output, stderr.open("w", encoding="utf-8") as error:
        try:
            if executable is None:
                raise FileNotFoundError(args[0])
            result = subprocess.run([executable, *args[1:]], cwd=cwd, env=env,
                                    stdout=output, stderr=error, timeout=900, check=False)
            code, failure = result.returncode, None
        except (OSError, subprocess.TimeoutExpired) as exc:
            code, failure = None, str(exc)
            error.write(failure)
    print(f"{name}: {'PASS' if code == 0 else 'FAIL'}", flush=True)
    return {"name": name, "command": args, "cwd": str(cwd.relative_to(ROOT) or "."),
            "exit_code": code, "failure": failure, "seconds": round(time.monotonic() - started, 3),
            "stdout": stdout.name, "stderr": stderr.name}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    out = args.out.resolve()
    out.mkdir(parents=True, exist_ok=False)
    before = source_hashes()
    manifest = {"protocol": "personal-learning-evaluation-v1", "started_at": now(),
                "source_sha256": before, "origin": "engineering_and_synthetic_regression",
                "real_user_efficacy": "not_evaluated"}
    (out / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
    env = os.environ.copy()
    env["WEKNORA_PLAN_AUDIT_PATH"] = str(out / "planner.json")
    commands = [
        ("backend", ["go", "test", "-json", "-count=1", *PACKAGES], ROOT),
        ("assessment", [sys.executable, "-m", "unittest", "test_learning_assessment",
                        "test_evaluate_learning_prototype", "-v"], ROOT / "scripts"),
        ("frontend", ["npm", "test"], ROOT / "frontend"),
    ]
    checks = [run_check(name, cmd, cwd, out, env) for name, cmd, cwd in commands]
    for check in checks[1:]:
        log = "\n".join((out / check[field]).read_text(encoding="utf-8", errors="replace") for field in ("stdout", "stderr"))
        check["observed_counts"] = observed_counts(log, check["name"])
        count = check["observed_counts"]
        check["execution_observed"] = count.get("tests", 0) > 0 and (
            check["name"] == "assessment" or (count.get("fail") == 0 and count.get("cancelled") == 0))
    parsed = go_results((out / "backend.stdout.log").read_text(encoding="utf-8", errors="replace"))
    learning_package = "github.com/Tencent/WeKnora/internal/application/service/learning/"
    outcomes = {key[len(learning_package):]: value for key, value in parsed["tests"].items()
                if key.startswith(learning_package)}
    skipped_go = [key for key, value in parsed["tests"].items() if value == "skip"]
    go_counts = {state: sum(value == state for value in parsed["tests"].values())
                 for state in ("pass", "fail", "skip")}
    axes = {axis: {name: outcomes.get(name, "not_executed") for name in tests}
            for axis, tests in AXES.items()}
    planner, planner_error = read_planner(out / "planner.json")
    changed = before != source_hashes()
    complete = (all(c["exit_code"] == 0 for c in checks) and not changed and planner is not None
                and all(c["execution_observed"] for c in checks[1:])
                and not skipped_go
                and checks[-1]["observed_counts"].get("skipped", 0) == 0
                and checks[-1]["observed_counts"].get("todo", 0) == 0
                and all(v == "pass" for tests in axes.values() for v in tests.values()))
    report = {**manifest, "finished_at": now(), "source_changed_during_run": changed,
              "engineering_checks_complete": complete, "checks": checks, "go": parsed,
              "go_result_records": go_counts, "skipped_go_checks": skipped_go,
              "axis_checks": axes, "planner": planner,
              "planner_parse_error": planner_error,
              "limits": ["工程通过率不是学习效果评分", "合成小图已在开发中使用，属于回归评估",
                         "规划比较仅针对声明效用", "真人效果、材料语义正确率、概率校准未验证"]}
    (out / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    lines = ["# 个人学习原型验证结果", "", f"工程检查：{'完成并通过' if complete else '未全部完成或存在失败'}。",
             "", "真实用户学习效果：尚未评估。测试数量不换算为项目分数。", "",
             "| 检查 | 结果 | 日志 |", "|---|---|---|"]
    for c in checks:
        passed = c['exit_code'] == 0 and c.get('execution_observed', True)
        label = '已执行项通过' if passed else '失败/未确认执行'
        if c['name'] == 'backend' and skipped_go:
            label += '，存在跳过项'
        lines.append(f"| {c['name']} | {label} | [{c['stdout']}]({c['stdout']})、[{c['stderr']}]({c['stderr']}) |")
    lines.extend(["", f"Go 结果记录：{go_counts['pass']} 通过、{go_counts['fail']} 失败、{go_counts['skip']} 跳过。"
                  "包含父测试与子测试记录，不是独立用例总数。"])
    if skipped_go:
        lines.extend(["", "以下检查未执行，不计为通过；PostgreSQL 专项检查需要专用测试数据库：", ""])
        lines.extend(f"- `{name}`" for name in skipped_go)
    frontend_counts = checks[-1]["observed_counts"]
    lines.append(f"前端：{frontend_counts.get('tests', '未知')} 条测试，"
                 f"{frontend_counts.get('skipped', '未知')} 跳过，{frontend_counts.get('todo', '未知')} 待实现。")
    lines.extend(["", f"运行期间源码变化：{'是，结果不可用于版本结论' if changed else '未检测到'}。", ""])
    for axis, tests in axes.items():
        lines.append(f"- {axis}：指定边界检查 {sum(v == 'pass' for v in tests.values())}/{len(tests)}；详情见 JSON。")
    lines.append("- 呈现：见前端完整测试日志；仍需真人页面可用性验收。")
    if planner:
        trials = planner.get("small_trials", [])
        optimal = sum(t["search"] == t["oracle"] for t in trials)
        lines.extend(["", f"小图穷举比较：{optimal}/{len(trials)} 达到声明效用的最优值；"
                      f"相对贪心严格改善 {planner.get('strict_improvements')} 例。",
                      f"贪心总效用差距 {planner.get('greedy_total_regret')}，"
                      f"当前搜索总效用差距 {planner.get('search_total_regret')}。",
                      "这些是合成图上的算法结果，不是学习成绩提升。", "",
                      "| 节点数 | 用时（秒） | 贪心效用 | 当前效用 | 上界 |", "|---|---|---|---|---|"])
        for row in planner.get("scale", []):
            plan = row["plan"]
            lines.append(f"| {row['nodes']} | {row['seconds']:.3f} | {row['greedy']} | {plan.get('utility')} | {plan.get('upper_bound')} |")
    else:
        lines.extend(["", "规划原始结果缺失，不报告规划优越性。"])
    lines.extend(["", "源码哈希、全部测试/跳过/失败、规模案例及边界声明见 report.json；不省略不利结果。", ""])
    (out / "report.md").write_text("\n".join(lines), encoding="utf-8")
    return 0 if complete else 1


if __name__ == "__main__":
    raise SystemExit(main())
