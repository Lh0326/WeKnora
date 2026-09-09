-- Migration 000089 (down): the epoch fence is infrastructure, not personal
-- data — dropping it only removes the delete/write serialization, no rows
-- of the person's learning data reference it.

DROP TABLE IF EXISTS learning_subject_epochs;
