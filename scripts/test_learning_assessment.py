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
    def capture(self):
        path=str(self.root/"capture.json")
        with patch.dict(os.environ,{"WEKNORA_EVAL_TOKEN":"fixture-token"}),patch.object(runner,"urlopen",return_value=io.StringIO(json.dumps({"data":self.frozen}))) as request:
            runner.capture(SimpleNamespace(base="http://localhost:8080",kb="kb",participant="p1",group="guided",origin="engineering_fixture",bank=self.bankpath,out=path))
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
if __name__=="__main__":unittest.main()
