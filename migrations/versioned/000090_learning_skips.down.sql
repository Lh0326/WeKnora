-- Migration 000090 (down): drop the user-declared skip list.
DROP INDEX IF EXISTS idx_learning_skips_scope_subject;
DROP INDEX IF EXISTS idx_learning_skips_scope;
DROP TABLE IF EXISTS learning_skips;
