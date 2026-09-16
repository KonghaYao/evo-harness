-- 已有库补 jobs 上的 majority agent/model；新库 001 已含这些列，重复执行时忽略 duplicate column。
ALTER TABLE jobs ADD COLUMN agent_name TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN agent_version TEXT;
ALTER TABLE jobs ADD COLUMN model_name TEXT;
ALTER TABLE jobs ADD COLUMN model_provider TEXT;
CREATE INDEX IF NOT EXISTS jobs_agent_name_idx ON jobs (agent_name);
CREATE INDEX IF NOT EXISTS jobs_model_name_idx ON jobs (model_name);
