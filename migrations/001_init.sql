PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS jobs (
    job_id TEXT PRIMARY KEY,
    job_name TEXT NOT NULL,
    s3_prefix TEXT NOT NULL,
    started_at TEXT,
    finished_at TEXT,
    n_trials INTEGER NOT NULL,
    n_errors INTEGER NOT NULL DEFAULT 0,
    n_retries INTEGER NOT NULL DEFAULT 0,
    n_reward_1 INTEGER NOT NULL,
    pass_at_1 REAL NOT NULL,
    n_input_tokens INTEGER NOT NULL DEFAULT 0,
    n_cache_tokens INTEGER NOT NULL DEFAULT 0,
    n_output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL NOT NULL DEFAULT 0,
    n_agent_steps INTEGER NOT NULL DEFAULT 0,
    usage_reported INTEGER NOT NULL DEFAULT 0,
    agent_name TEXT NOT NULL DEFAULT '',
    agent_version TEXT,
    model_name TEXT,
    model_provider TEXT,
    ingest_sha256 TEXT,
    ingested_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS jobs_ingested_at_idx ON jobs (ingested_at DESC);
CREATE INDEX IF NOT EXISTS jobs_job_name_idx ON jobs (job_name);

CREATE TABLE IF NOT EXISTS trials (
    trial_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL REFERENCES jobs (job_id) ON DELETE CASCADE,
    task_name TEXT NOT NULL,
    task_checksum TEXT NOT NULL,
    trial_name TEXT,
    task_id TEXT,
    source TEXT,
    trial_uri TEXT,
    agent_name TEXT NOT NULL,
    agent_version TEXT,
    model_name TEXT,
    model_provider TEXT,
    agent_info TEXT NOT NULL,
    reward REAL NOT NULL,
    f2p REAL,
    p2p REAL,
    verifier_rewards TEXT NOT NULL,
    exception_type TEXT,
    exception_info TEXT,
    started_at TEXT,
    finished_at TEXT,
    environment_setup TEXT,
    agent_setup TEXT,
    agent_execution TEXT,
    verifier_timing TEXT,
    n_input_tokens INTEGER NOT NULL DEFAULT 0,
    n_cache_tokens INTEGER NOT NULL DEFAULT 0,
    n_output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd REAL NOT NULL DEFAULT 0,
    n_agent_steps INTEGER NOT NULL DEFAULT 0,
    usage_reported INTEGER NOT NULL DEFAULT 0,
    trajectory_uri TEXT,
    s3_trial_prefix TEXT
);

CREATE INDEX IF NOT EXISTS trials_job_id_idx ON trials (job_id);
CREATE INDEX IF NOT EXISTS trials_job_reward_idx ON trials (job_id, reward);
CREATE INDEX IF NOT EXISTS trials_job_checksum_idx ON trials (job_id, task_checksum);

CREATE TABLE IF NOT EXISTS job_overlays (
    job_id TEXT PRIMARY KEY REFERENCES jobs (job_id) ON DELETE CASCADE,
    runner_name TEXT,
    runner_version TEXT,
    sandbox_type TEXT,
    sandbox_location TEXT,
    endpoint_class TEXT,
    job_type TEXT,
    job_type_reason TEXT,
    attestation_status TEXT NOT NULL DEFAULT 'unsigned',
    attestor TEXT,
    hub_org TEXT,
    harbor_version TEXT,
    dataset_name TEXT,
    dataset_version TEXT,
    dataset_ref TEXT,
    dataset_path TEXT,
    incomparability TEXT,
    extra TEXT NOT NULL DEFAULT '{}',
    listed INTEGER NOT NULL DEFAULT 1,
    CHECK (job_type IS NULL OR job_type IN ('H', 'M'))
);

CREATE TABLE IF NOT EXISTS hub_job (
    id TEXT PRIMARY KEY,
    job_name TEXT,
    config TEXT,
    visibility TEXT,
    started_at TEXT,
    finished_at TEXT,
    archive_path TEXT,
    log_path TEXT,
    n_planned_trials INTEGER,
    org_id TEXT,
    created_by TEXT,
    is_hosted INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS hub_trial (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    trial_name TEXT,
    task_name TEXT,
    task_content_hash TEXT,
    lock TEXT,
    agent_id TEXT,
    config TEXT,
    rewards TEXT,
    exception_type TEXT,
    environment_setup TEXT,
    agent_setup TEXT,
    agent_execution TEXT,
    verifier TEXT,
    archive_path TEXT,
    trajectory_path TEXT
);

CREATE INDEX IF NOT EXISTS hub_trial_job_id_idx ON hub_trial (job_id);

CREATE TABLE IF NOT EXISTS hub_agent (
    id TEXT PRIMARY KEY,
    added_by TEXT NOT NULL,
    name TEXT NOT NULL,
    version TEXT NOT NULL,
    UNIQUE (added_by, name, version)
);

CREATE TABLE IF NOT EXISTS hub_model (
    id TEXT PRIMARY KEY,
    added_by TEXT NOT NULL,
    name TEXT NOT NULL,
    provider TEXT NOT NULL DEFAULT 'unknown',
    UNIQUE (added_by, name, provider)
);

CREATE TABLE IF NOT EXISTS hub_trial_model (
    trial_id TEXT NOT NULL,
    model_id TEXT NOT NULL,
    n_input_tokens INTEGER,
    n_cache_tokens INTEGER,
    n_output_tokens INTEGER,
    cost_usd REAL,
    PRIMARY KEY (trial_id, model_id)
);
