package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestOpenWALAndSerialWrites(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	if err := st.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := st.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
				id := fmt.Sprintf("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa%d", i)
				name := fmt.Sprintf("job-%d", i)
				return InsertHubJob(ctx, tx, HubJob{ID: id, JobName: &name})
			})
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenMaxOpenConns(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.DB.Stats().MaxOpenConnections != 16 {
		t.Fatalf("MaxOpenConns=%d", st.DB.Stats().MaxOpenConnections)
	}
}

func TestBackfillJobAgentModelOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	jobID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	ver, model, prov := "agent-v3.14.2", "deepseek-v4-flash", "deepseek"
	name := "t1"
	job := AnalysisJob{JobID: jobID, JobName: "backfill-job", S3Prefix: "jobs/" + jobID + "/", NTrials: 2, NReward1: 1, PassAt1: 0.5, IngestedAt: NowRFC3339()}
	trials := []AnalysisTrial{
		{TrialID: "11111111-1111-4111-8111-111111111111", JobID: jobID, TaskName: "a", TaskChecksum: "c1", TrialName: &name, AgentName: "peri", AgentVersion: &ver, ModelName: &model, ModelProvider: &prov, AgentInfo: `{"name":"peri"}`, Reward: 1, VerifierRewards: `{"reward":1}`},
		{TrialID: "11111111-1111-4111-8111-111111111112", JobID: jobID, TaskName: "b", TaskChecksum: "c2", AgentName: "peri", AgentVersion: &ver, ModelName: &model, ModelProvider: &prov, AgentInfo: `{"name":"peri"}`, Reward: 0, VerifierRewards: `{"reward":0}`},
	}
	if err := st.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return InsertFinalized(ctx, tx, job, trials, Overlay{JobID: jobID})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`UPDATE jobs SET agent_name='', agent_version=NULL, model_name=NULL, model_provider=NULL`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	got, err := st2.GetAnalysisJob(ctx, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentName != "peri" {
		t.Fatalf("backfill agent_name %q", got.AgentName)
	}
	if got.AgentVersion == nil || *got.AgentVersion != ver {
		t.Fatalf("backfill agent_version %v", got.AgentVersion)
	}
	if got.ModelName == nil || *got.ModelName != model {
		t.Fatalf("backfill model_name %v", got.ModelName)
	}
	if got.ModelProvider == nil || *got.ModelProvider != prov {
		t.Fatalf("backfill model_provider %v", got.ModelProvider)
	}
}

func TestListJobsPlanDoesNotScanTrials(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()
	jobID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	ver, model, prov := "v", "m", "p"
	job := AnalysisJob{JobID: jobID, JobName: "plan-job", S3Prefix: "jobs/" + jobID + "/", NTrials: 1, NReward1: 1, PassAt1: 1, IngestedAt: NowRFC3339()}
	trials := []AnalysisTrial{{
		TrialID: "11111111-1111-4111-8111-111111111201", JobID: jobID, TaskName: "a", TaskChecksum: "c",
		AgentName: "peri", AgentVersion: &ver, ModelName: &model, ModelProvider: &prov,
		AgentInfo: `{"name":"peri"}`, Reward: 1, VerifierRewards: `{"reward":1}`,
	}}
	if err := st.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return InsertFinalized(ctx, tx, job, trials, Overlay{JobID: jobID})
	}); err != nil {
		t.Fatal(err)
	}
	before := st.TrialReadCount()
	rows, total, err := st.ListJobs(ctx, ListJobsFilter{ListedOnly: true, AgentName: strp("peri"), ModelName: strp("m"), Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if st.TrialReadCount() != before {
		t.Fatalf("ListJobs trial reads %d -> %d", before, st.TrialReadCount())
	}
	if total != 1 || len(rows) != 1 || rows[0].AgentName != "peri" {
		t.Fatalf("list %+v total=%d", rows, total)
	}
	plan, err := st.DB.Query(`EXPLAIN QUERY PLAN SELECT j.job_id FROM jobs j LEFT JOIN job_overlays o ON o.job_id = j.job_id WHERE COALESCE(o.listed, 1) = 1 AND j.agent_name = ? AND j.model_name = ? ORDER BY j.ingested_at DESC LIMIT 50 OFFSET 0`, "peri", "m")
	if err != nil {
		t.Fatal(err)
	}
	defer plan.Close()
	var details []string
	cols, err := plan.Columns()
	if err != nil {
		t.Fatal(err)
	}
	for plan.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := plan.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		details = append(details, fmt.Sprint(vals))
	}
	joined := strings.ToLower(strings.Join(details, " | "))
	if strings.Contains(joined, "trial") {
		t.Fatalf("list plan must not touch trials: %s", joined)
	}
}

func TestOpenMigratesLegacyJobsTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)")
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
CREATE TABLE jobs (
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
    ingest_sha256 TEXT,
    ingested_at TEXT NOT NULL
);
CREATE TABLE trials (
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
CREATE TABLE job_overlays (
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
    listed INTEGER NOT NULL DEFAULT 1
);
INSERT INTO jobs (job_id, job_name, s3_prefix, n_trials, n_reward_1, pass_at_1, ingested_at)
VALUES ('dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'legacy-job', 'jobs/dddddddd-dddd-4ddd-8ddd-dddddddddddd/', 1, 1, 1.0, '2026-09-16T00:00:00Z');
INSERT INTO trials (trial_id, job_id, task_name, task_checksum, trial_name, agent_name, agent_version, model_name, model_provider, agent_info, reward, verifier_rewards)
VALUES ('11111111-1111-4111-8111-111111111301', 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', 't', 'sum', 't1', 'peri', 'agent-v3.14.2', 'deepseek-v4-flash', 'deepseek', '{"name":"peri"}', 1, '{"reward":1}');
INSERT INTO job_overlays (job_id, attestation_status, extra, listed) VALUES ('dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'unsigned', '{}', 1);
`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	got, err := st.GetAnalysisJob(context.Background(), "dddddddd-dddd-4ddd-8ddd-dddddddddddd")
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentName != "peri" || got.ModelName == nil || *got.ModelName != "deepseek-v4-flash" {
		t.Fatalf("legacy migrate/backfill: %+v", got)
	}
	before := st.TrialReadCount()
	rows, total, err := st.ListJobs(context.Background(), ListJobsFilter{ListedOnly: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if st.TrialReadCount() != before {
		t.Fatal("legacy list scanned trials")
	}
	if total != 1 || rows[0].AgentName != "peri" {
		t.Fatalf("legacy list %+v total=%d", rows, total)
	}
}

func strp(s string) *string { return &s }
