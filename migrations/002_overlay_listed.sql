-- 已有库补 listed；新库 001 已含该列，重复执行时忽略 duplicate column。
ALTER TABLE job_overlays ADD COLUMN listed INTEGER NOT NULL DEFAULT 1;
