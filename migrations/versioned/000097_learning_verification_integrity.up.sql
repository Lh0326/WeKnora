-- Verification provenance; legacy rows remain unverified.
ALTER TABLE learning_objectives ADD COLUMN evidence_hash VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_items ADD COLUMN objective_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_tasks ADD COLUMN objective_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_tasks ADD COLUMN evidence_hash VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN contract_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN rubric_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN scorer_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_quiz_attempts ADD COLUMN item_content_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_task_attempts ADD COLUMN objective_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_task_attempts ADD COLUMN contract_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_task_attempts ADD COLUMN rubric_version VARCHAR(128) NOT NULL DEFAULT '';
ALTER TABLE learning_task_attempts ADD COLUMN scorer_version VARCHAR(128) NOT NULL DEFAULT '';

-- The former seeder manufactured approvals from a command-line identity. Preserve its note,
-- but require actual content review. This exact automation stamp is not human attestation.
UPDATE learning_objectives SET status='draft', reviewer='', published_at=NULL
 WHERE review_note='演示路径内容包：按 2026-09-09 阶段2 验收指令由操作者代内容所有者审核；欢迎复审（ReviewQuizItem 再次调用即可）。';
UPDATE learning_quiz_items SET status='draft', reviewer='', published_at=NULL
 WHERE review_note='演示路径内容包：按 2026-09-09 阶段2 验收指令由操作者代内容所有者审核；欢迎复审（ReviewQuizItem 再次调用即可）。';
UPDATE learning_tasks SET status='draft', reviewer='', published_at=NULL
 WHERE review_note='演示路径内容包：按 2026-09-09 阶段2 验收指令由操作者代内容所有者审核；欢迎复审（ReviewQuizItem 再次调用即可）。';
