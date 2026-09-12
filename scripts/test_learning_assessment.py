import contextlib
import io
import json
import os
from pathlib import Path
from tempfile import TemporaryDirectory
from types import SimpleNamespace
import unittest
from unittest.mock import patch
import learning_assessment as runner

class AssessmentRunnerTests(unittest.TestCase):
    def setUp(self):
        self.tmp=TemporaryDirectory(); self.addCleanup(self.tmp.cleanup); self.root=Path(self.tmp.name)
        self.bank={"frozen_rules":"fixture-v1","items":[{"id":"holdout","objective_id":"o","objective_version":"v1","family_id":"holdout-family","phase":"posttest","scenario":"Choose the correct fixture value", "assistance_mode":"closed_book","reviewer":"test fixture only","status":"published","rubric_version":"v1","source_refs":["fixture-source"],"fields":[{"id":"choice","label":"Choose","options":["x","y"]}],"answer_key":{"choice":"x"}}]}
        self.bankpath=self.write("bank.json",self.bank)
        self.frozen={"snapshot_id":"server-id","knowledge_base_id":"kb","policy_version":"v2","state_as_of":"2026-01-01T00:00:00Z","goal_states_before":{"o":"verified","unseen":"unverified"},"objective_versions":{"o":"v1","unseen":"v1"},"evidence_families_before":["training-family"]}
    def write(self,name,value):
        path=str(self.root/name);runner.save(path,value);return path
    def capture(self, name="capture.json", **kwargs):
        path=str(self.root/name)
        with patch.dict(os.environ,{"WEKNORA_EVAL_TOKEN":"fixture-token"}),patch.object(runner,"urlopen",return_value=io.StringIO(json.dumps({"data":self.frozen}))) as request:
            options=dict(base="http://localhost:8080",kb="kb",participant="p1",group="guided",origin="engineering_fixture",bank=self.bankpath,out=path)
            options.update(kwargs); runner.capture(SimpleNamespace(**options))
            self.assertEqual(request.call_args[0][0].headers["Authorization"],"Bearer fixture-token")
        self.assertNotIn("fixture-token",Path(path).read_text())
        return path
    def complete(self,assisted=False):
        capture=self.capture(); presentation=str(self.root/"present.json");grade=str(self.root/"grade.json")
        out=io.StringIO()
        with contextlib.redirect_stdout(out):runner.present(SimpleNamespace(capture=capture,bank=self.bankpath,item="holdout",out=presentation))
        self.assertNotIn("answer_key",out.getvalue())
        answers=self.write("answers.json",{"choice":"x"})
        runner.grade(SimpleNamespace(presentation=presentation,bank=self.bankpath,answers=answers,assisted=assisted,out=grade))
        return capture,presentation,grade
    def test_full_chain_preserves_frozen_denominator(self):
        capture,_,grade=self.complete();out=str(self.root/"assess.json")
        runner.assemble(SimpleNamespace(captures=[capture],grades=[grade],out=out,seed=4,tuning=False))
        result=runner.load(out);p=result["participants"][0]
        self.assertEqual(len(p["goal_states_before"]),2);self.assertEqual(p["posttest_score"],1);self.assertFalse(p["missing_post"])
        self.assertEqual(p["snapshots"][0]["state_before"],"verified");self.assertEqual(result["data_origin"],"engineering_fixture")
    def test_assisted_is_missing_primary_not_a_success(self):
        capture,_,grade=self.complete(True);out=str(self.root/"assess.json")
        runner.assemble(SimpleNamespace(captures=[capture],grades=[grade],out=out,seed=4,tuning=False))
        p=runner.load(out)["participants"][0];self.assertTrue(p["missing_post"]);self.assertNotIn("posttest_score",p)
    def test_frozen_files_cannot_be_overwritten_or_tampered(self):
        path=self.capture()
        with self.assertRaises(FileExistsError):runner.save(path,{})
        artifact=runner.load(path);artifact["record"]["group"]="baseline";Path(path).write_text(json.dumps(artifact))
        with self.assertRaises(ValueError):runner.unseal(path)
    def test_bank_change_after_freeze_rejected(self):
        path=self.capture();self.bank["items"][0]["answer_key"]["choice"]="y";changed=self.write("changed.json",self.bank)
        with self.assertRaises(ValueError):runner.present(SimpleNamespace(capture=path,bank=changed,item="holdout",out=str(self.root/"p.json")))
    def test_exposed_family_rejected_before_presentation(self):
        self.frozen["evidence_families_before"].append("holdout-family")
        with self.assertRaises(ValueError):self.capture()
    def test_duplicate_results_and_missing_users_retained(self):
        capture,_,grade=self.complete()
        with self.assertRaises(ValueError):runner.assemble(SimpleNamespace(captures=[capture],grades=[grade,grade],out=str(self.root/"a.json"),seed=1,tuning=False))
        out=str(self.root/"missing.json");runner.assemble(SimpleNamespace(captures=[capture],grades=[],out=out,seed=1,tuning=False))
        self.assertTrue(runner.load(out)["participants"][0]["missing_post"])

    def component_setup(self):
        self.bank["target_type"]="component"
        item=self.bank["items"][0]
        item["component_id"]=item.pop("objective_id");item["component_version"]=item.pop("objective_version")
        self.bankpath=self.write("component-bank.json",self.bank)
        self.frozen.update(component_states_before={"o":"familiar","unseen":"unseen"},
                           component_versions={"o":"v1","unseen":"v1"},component_available={"o":True,"unseen":True},
                           component_evidence_families_before=["training-family"],component_model_version="kc-evidence-separated-v2",
                           component_exposure_complete=False)

    def test_component_full_chain_keeps_raw_state_and_descriptive_denominators(self):
        self.component_setup();capture,_,grade=self.complete()
        out=str(self.root/"component-input.json")
        runner.assemble(SimpleNamespace(captures=[capture],grades=[grade],out=out,seed=4,tuning=False))
        data=runner.load(out);self.assertEqual(data["target_type"],"component")
        snapshot=data["participants"][0]["snapshots"][0]
        self.assertEqual(snapshot["state_before"],"familiar");self.assertEqual(snapshot["component_id"],"o")
        self.assertNotIn("objective_id",snapshot);self.assertIn("source_refs",snapshot)
        report=str(self.root/"description.json")
        runner.describe(SimpleNamespace(captures=[capture],grades=[grade],out=report))
        result=runner.load(report)
        self.assertEqual(result["participant_count"],1);self.assertIsNone(result["population_effect"])
        self.assertEqual(result["posttest_by_state"]["familiar"]["label"],"检查支持")
        self.assertEqual(result["check_supported_posttest_trials"],1)
        self.assertEqual(result["blocks"][0]["score_percent"],100)
        self.assertFalse(result["blocks"][0]["exposure_complete"])

    def test_component_version_source_and_exposure_are_gates(self):
        self.component_setup();self.frozen["component_available"]["o"]=False
        with self.assertRaisesRegex(ValueError,"source is unavailable"):self.capture()
        self.frozen["component_available"]["o"]=True;self.frozen["component_versions"]["o"]="changed"
        with self.assertRaisesRegex(ValueError,"version"):self.capture()
        self.frozen["component_versions"]["o"]="v1";self.frozen["component_evidence_families_before"].append("holdout-family")
        with self.assertRaisesRegex(ValueError,"exposed"):self.capture()

    def test_capture_phase_assigns_only_that_phase_and_rejects_wrong_target(self):
        self.component_setup()
        pre=dict(self.bank["items"][0],id="pre",family_id="pre-family",phase="pretest")
        self.bank["items"].append(pre);self.bankpath=self.write("phases.json",self.bank)
        capture=self.capture(phase="pretest",target_type="component")
        data,_=runner.unseal(capture);self.assertEqual([item["id"] for item in data["assigned_items"]],["pre"])
        with self.assertRaisesRegex(ValueError,"not assigned"):
            runner.present(SimpleNamespace(capture=capture,bank=self.bankpath,item="holdout",out=str(self.root/"bad-present.json")))
        with self.assertRaisesRegex(ValueError,"target type"):
            self.capture("wrong.json",target_type="objective")

    def test_first_answer_cannot_be_replaced_by_another_output_file(self):
        capture,presentation,grade=self.complete()
        wrong=self.write("wrong-answer.json",{"choice":"y"})
        with self.assertRaisesRegex(ValueError,"first answer"):
            runner.grade(SimpleNamespace(presentation=presentation,bank=self.bankpath,answers=wrong,assisted=False,out=str(self.root/"retry.json")))
        # Re-presenting through another output path retains the original timestamp/journal.
        reopened=str(self.root/"reopened.json")
        with contextlib.redirect_stdout(io.StringIO()):
            runner.present(SimpleNamespace(capture=capture,bank=self.bankpath,item="holdout",out=reopened))
        self.assertEqual(runner.unseal(reopened)[0]["presented_at"],runner.unseal(presentation)[0]["presented_at"])
        with self.assertRaisesRegex(ValueError,"first answer"):
            runner.grade(SimpleNamespace(presentation=reopened,bank=self.bankpath,answers=wrong,assisted=False,out=str(self.root/"retry2.json")))
        original,_=runner.unseal(grade);same=str(self.root/"same.json")
        runner.grade(SimpleNamespace(presentation=reopened,bank=self.bankpath,answers=str(self.root/"answers.json"),assisted=False,out=same))
        self.assertEqual(runner.unseal(same)[0],original)

    def test_wall_time_uses_timezone_instants_and_is_not_learning_time(self):
        capture=self.capture();present=str(self.root/"time-present.json");grade=str(self.root/"time-grade.json")
        with patch.object(runner,"now",return_value="2026-01-01T08:00:01+08:00"),contextlib.redirect_stdout(io.StringIO()):
            runner.present(SimpleNamespace(capture=capture,bank=self.bankpath,item="holdout",out=present))
        answers=self.write("time-answers.json",{"choice":"x"})
        with patch.object(runner,"now",return_value="2026-01-01T00:00:16Z"):
            runner.grade(SimpleNamespace(presentation=present,bank=self.bankpath,answers=answers,assisted=False,out=grade))
        record,_=runner.unseal(grade);self.assertEqual(record["snapshot"]["response_wall_seconds"],15)
        out=str(self.root/"timing.json");runner.describe(SimpleNamespace(captures=[capture],grades=[grade],out=out))
        result=runner.load(out);self.assertEqual(result["blocks"][0]["response_wall_seconds"],15)
        self.assertIsNone(result["learning_time_seconds"])

    def test_one_person_multiple_blocks_stays_one_and_missing_is_not_wrong(self):
        self.component_setup();capture,_,grade=self.complete(True)
        self.frozen["snapshot_id"]="server-second"
        second=self.capture("second.json",group="baseline",block="second")
        out=str(self.root/"blocks.json")
        runner.describe(SimpleNamespace(captures=[capture,second],grades=[grade],out=out))
        result=runner.load(out)
        self.assertEqual(result["participant_count"],1);self.assertEqual(len(result["blocks"]),2)
        self.assertEqual(len(result["exclusions"]),1)
        self.assertTrue(all(block["score_percent"] is None for block in result["blocks"]))
        self.assertTrue(any(block["missing_item_ids"]==["holdout"] for block in result["blocks"]))
        with self.assertRaisesRegex(ValueError,"duplicate participant"):
            runner.assemble(SimpleNamespace(captures=[capture,second],grades=[grade],out=str(self.root/"bad-multi.json"),seed=4,tuning=False))

    def test_description_rejects_multiple_people_and_tampered_state(self):
        self.component_setup();capture,_,grade=self.complete()
        self.frozen["snapshot_id"]="another-server"
        other=self.capture("other.json",participant="p2")
        with self.assertRaisesRegex(ValueError,"exactly one"):
            runner.describe(SimpleNamespace(captures=[capture,other],grades=[grade],out=str(self.root/"people.json")))
        record,_=runner.unseal(grade);record["snapshot"]["state_before"]="verified"
        tampered=self.write("tampered-grade.json",runner.seal(record))
        with self.assertRaisesRegex(ValueError,"does not match"):
            runner.describe(SimpleNamespace(captures=[capture],grades=[tampered],out=str(self.root/"tampered-report.json")))

    def test_browser_receipt_keeps_submission_time_and_cannot_hide_help(self):
        capture=self.capture();present=str(self.root/"receipt-present.json")
        with patch.object(runner,"now",return_value="2026-01-01T00:00:01Z"),contextlib.redirect_stdout(io.StringIO()):
            runner.present(SimpleNamespace(capture=capture,bank=self.bankpath,item="holdout",out=present))
        receipt=self.write("receipt.json",{"answers":{"choice":"x"},"submitted_at":"2026-01-01T00:00:11Z","assisted":True})
        grade=str(self.root/"receipt-grade.json")
        with patch.object(runner,"now",return_value="2026-01-01T00:05:00Z"):
            runner.grade(SimpleNamespace(presentation=present,bank=self.bankpath,answers=receipt,assisted=False,out=grade))
        snapshot=runner.unseal(grade)[0]["snapshot"]
        self.assertEqual(snapshot["response_wall_seconds"],10);self.assertFalse(snapshot["eligible"])
        self.assertEqual(snapshot["timing_basis"],"local_submission_receipt")
        future=self.write("future-receipt.json",{"answers":{"choice":"x"},"submitted_at":"2026-01-01T00:06:00Z"})
        with patch.object(runner,"now",return_value="2026-01-01T00:05:00Z"),self.assertRaises(ValueError):
            runner.grade(SimpleNamespace(presentation=present,bank=self.bankpath,answers=future,assisted=False,out=str(self.root/"future-grade.json")))

    def test_downloaded_freeze_needs_no_token_and_keeps_same_scope_gates(self):
        self.component_setup();path=self.write("download.json",{"success":True,"data":self.frozen})
        out=str(self.root/"download-capture.json")
        args=SimpleNamespace(base=None,freeze_file=path,kb="kb",participant="p1",group="guided",origin="engineering_fixture",bank=self.bankpath,out=out)
        with patch.dict(os.environ,{},clear=True),patch.object(runner,"urlopen") as network:
            runner.capture(args);network.assert_not_called()
        record,_=runner.unseal(out)
        self.assertEqual(record["frozen"],self.frozen);self.assertIn("not cryptographically authenticated",record["freeze_source"])
        args.kb="other-kb";args.out=str(self.root/"wrong-scope.json")
        with self.assertRaisesRegex(ValueError,"invalid freeze"):runner.capture(args)

    def test_simultaneous_question_windows_are_not_added_as_double_time(self):
        self.bank["items"].append(dict(self.bank["items"][0],id="holdout2",family_id="family2"))
        self.bankpath=self.write("two-items.json",self.bank);capture=self.capture();grades=[]
        answer=self.write("timed-answer.json",{"answers":{"choice":"x"},"submitted_at":"2026-01-01T00:01:01Z","assisted":False})
        for index,item in enumerate(self.bank["items"]):
            presentation=str(self.root/f"parallel-{index}.presented.json");grade=str(self.root/f"parallel-{index}.graded.json")
            with patch.object(runner,"now",return_value="2026-01-01T00:00:01Z"),contextlib.redirect_stdout(io.StringIO()):
                runner.present(SimpleNamespace(capture=capture,bank=self.bankpath,item=item["id"],out=presentation))
            with patch.object(runner,"now",return_value="2026-01-01T00:05:00Z"):
                runner.grade(SimpleNamespace(presentation=presentation,bank=self.bankpath,answers=answer,assisted=False,out=grade))
            grades.append(grade)
        out=str(self.root/"parallel-report.json");runner.describe(SimpleNamespace(captures=[capture],grades=grades,out=out))
        report=runner.load(out);self.assertEqual(report["blocks"][0]["response_wall_seconds"],60)
        self.assertEqual(report["blocks"][0]["submitted"],2)
if __name__=="__main__":unittest.main()
