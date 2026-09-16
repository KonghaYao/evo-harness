package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"evo-harness/migrations"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

type Store struct {
	DB *sql.DB
	mu sync.Mutex
	// trialReads counts AllTrials / ListTrials / GetTrial. List/stats must not increment this.
	trialReads atomic.Int64
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is empty")
	}
	dsn := path
	if !strings.HasPrefix(path, "file:") && path != ":memory:" {
		dsn = "file:" + path
	}
	if path == ":memory:" {
		dsn = "file:eval-display?mode=memory&cache=shared"
	}
	if !strings.Contains(dsn, "?") {
		dsn += "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	} else if !strings.Contains(dsn, "_pragma") {
		dsn += "&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL allows concurrent readers; writes still go through s.mu (single writer).
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(0)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{DB: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.DB.Close() }

func (s *Store) Ping(ctx context.Context) error {
	return s.DB.QueryRowContext(ctx, "SELECT 1").Scan(new(int))
}

func (s *Store) migrate() error {
	entries, err := fs.ReadDir(migrations.SQL, ".")
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, name := range names {
		b, err := migrations.SQL.ReadFile(name)
		if err != nil {
			return err
		}
		for i, stmt := range splitSQL(string(b)) {
			if _, err := tx.Exec(stmt); err != nil {
				if strings.Contains(strings.ToLower(err.Error()), "duplicate column name") {
					continue
				}
				return fmt.Errorf("migration %s statement %d: %w", name, i, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.backfillJobAgentModelLocked()
}

// TrialReadCount is the number of trial-table full/list/get reads since Open.
func (s *Store) TrialReadCount() int64 { return s.trialReads.Load() }

func (s *Store) noteTrialRead() { s.trialReads.Add(1) }

func (s *Store) backfillJobAgentModelLocked() error {
	ctx := context.Background()
	rows, err := s.DB.QueryContext(ctx, `SELECT job_id FROM jobs WHERE agent_name = ''`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, jobID := range ids {
		trials, err := s.agentColsForJob(ctx, jobID)
		if err != nil {
			return err
		}
		id := MajorityAgentIdentity(trials)
		if id.AgentName == "" {
			continue
		}
		if _, err := s.DB.ExecContext(ctx, `UPDATE jobs SET agent_name=?, agent_version=?, model_name=?, model_provider=? WHERE job_id=? AND agent_name=''`,
			id.AgentName, id.AgentVersion, id.ModelName, id.ModelProvider, jobID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) agentColsForJob(ctx context.Context, jobID string) ([]AnalysisTrial, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT agent_name, agent_version, model_name, model_provider FROM trials WHERE job_id = ? ORDER BY trial_name, trial_id`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AnalysisTrial
	for rows.Next() {
		var t AnalysisTrial
		if err := rows.Scan(&t.AgentName, &t.AgentVersion, &t.ModelName, &t.ModelProvider); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Write(ctx context.Context, fn func(ctx context.Context, tx *sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

type HubJob struct {
	ID             string
	JobName        *string
	Config         *string
	Visibility     *string
	StartedAt      *string
	FinishedAt     *string
	ArchivePath    *string
	LogPath        *string
	NPlannedTrials *int64
	OrgID          *string
	CreatedBy      *string
	IsHosted       bool
}

type HubTrial struct {
	ID               string
	JobID            string
	TrialName        *string
	TaskName         *string
	TaskContentHash  *string
	Lock             *string
	AgentID          *string
	Config           *string
	Rewards          *string
	ExceptionType    *string
	EnvironmentSetup *string
	AgentSetup       *string
	AgentExecution   *string
	Verifier         *string
	ArchivePath      *string
	TrajectoryPath   *string
}

type HubAgent struct {
	ID      string
	AddedBy string
	Name    string
	Version string
}

type HubModel struct {
	ID       string
	AddedBy  string
	Name     string
	Provider string
}

type AnalysisJob struct {
	JobID         string
	JobName       string
	S3Prefix      string
	StartedAt     *string
	FinishedAt    *string
	NTrials       int
	NErrors       int
	NRetries      int
	NReward1      int
	PassAt1       float64
	NInputTokens  int64
	NCacheTokens  int64
	NOutputTokens int64
	CostUSD       float64
	NAgentSteps   int64
	UsageReported bool
	IngestSHA256  *string
	IngestedAt    string
	AgentName     string
	AgentVersion  *string
	ModelName     *string
	ModelProvider *string
}

type AnalysisTrial struct {
	TrialID          string
	JobID            string
	TaskName         string
	TaskChecksum     string
	TrialName        *string
	TaskID           *string
	Source           *string
	TrialURI         *string
	AgentName        string
	AgentVersion     *string
	ModelName        *string
	ModelProvider    *string
	AgentInfo        string
	Reward           float64
	F2P              *float64
	P2P              *float64
	VerifierRewards  string
	ExceptionType    *string
	ExceptionInfo    *string
	StartedAt        *string
	FinishedAt       *string
	EnvironmentSetup *string
	AgentSetup       *string
	AgentExecution   *string
	VerifierTiming   *string
	NInputTokens     int64
	NCacheTokens     int64
	NOutputTokens    int64
	CostUSD          float64
	NAgentSteps      int64
	UsageReported    bool
	TrajectoryURI    *string
	S3TrialPrefix    *string
}

type Overlay struct {
	JobID             string
	RunnerName        *string
	RunnerVersion     *string
	SandboxType       *string
	SandboxLocation   *string
	EndpointClass     *string
	JobType           *string
	JobTypeReason     *string
	AttestationStatus string
	Attestor          *string
	HubOrg            *string
	HarborVersion     *string
	DatasetName       *string
	DatasetVersion    *string
	DatasetRef        *string
	DatasetPath       *string
	Incomparability   *string
	Extra             string
	Listed            bool
}

func splitSQL(s string) []string {
	parts := strings.Split(s, ";")
	var out []string
	for _, p := range parts {
		var kept []string
		for _, line := range strings.Split(p, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "--") {
				continue
			}
			kept = append(kept, line)
		}
		stmt := strings.TrimSpace(strings.Join(kept, "\n"))
		if stmt == "" {
			continue
		}
		out = append(out, stmt)
	}
	return out
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func GetHubJob(ctx context.Context, q queryer, id string) (*HubJob, error) {
	row := q.QueryRowContext(ctx, `SELECT id, job_name, config, visibility, started_at, finished_at, archive_path, log_path, n_planned_trials, org_id, created_by, is_hosted FROM hub_job WHERE id = ?`, id)
	var j HubJob
	var hosted int
	err := row.Scan(&j.ID, &j.JobName, &j.Config, &j.Visibility, &j.StartedAt, &j.FinishedAt, &j.ArchivePath, &j.LogPath, &j.NPlannedTrials, &j.OrgID, &j.CreatedBy, &hosted)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	j.IsHosted = hosted != 0
	return &j, nil
}

func (s *Store) GetHubJob(ctx context.Context, id string) (*HubJob, error) {
	return GetHubJob(ctx, s.DB, id)
}

func InsertHubJob(ctx context.Context, tx *sql.Tx, j HubJob) error {
	hosted := 0
	if j.IsHosted {
		hosted = 1
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO hub_job (id, job_name, config, visibility, started_at, finished_at, archive_path, log_path, n_planned_trials, org_id, created_by, is_hosted)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		j.ID, j.JobName, j.Config, j.Visibility, j.StartedAt, j.FinishedAt, j.ArchivePath, j.LogPath, j.NPlannedTrials, j.OrgID, j.CreatedBy, hosted)
	return err
}

func PatchHubJob(ctx context.Context, tx *sql.Tx, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	var sets []string
	var args []any
	for k, v := range fields {
		sets = append(sets, k+" = ?")
		args = append(args, v)
	}
	args = append(args, id)
	_, err := tx.ExecContext(ctx, "UPDATE hub_job SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	return err
}

func GetHubTrial(ctx context.Context, q queryer, id string) (*HubTrial, error) {
	row := q.QueryRowContext(ctx, `SELECT id, job_id, trial_name, task_name, task_content_hash, lock, agent_id, config, rewards, exception_type, environment_setup, agent_setup, agent_execution, verifier, archive_path, trajectory_path FROM hub_trial WHERE id = ?`, id)
	var t HubTrial
	err := row.Scan(&t.ID, &t.JobID, &t.TrialName, &t.TaskName, &t.TaskContentHash, &t.Lock, &t.AgentID, &t.Config, &t.Rewards, &t.ExceptionType, &t.EnvironmentSetup, &t.AgentSetup, &t.AgentExecution, &t.Verifier, &t.ArchivePath, &t.TrajectoryPath)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func ListHubTrialsByJob(ctx context.Context, q queryer, jobID string, offset, limit int) ([]HubTrial, error) {
	rows, err := q.QueryContext(ctx, `SELECT id, job_id, trial_name, task_name, task_content_hash, lock, agent_id, config, rewards, exception_type, environment_setup, agent_setup, agent_execution, verifier, archive_path, trajectory_path FROM hub_trial WHERE job_id = ? ORDER BY trial_name LIMIT ? OFFSET ?`, jobID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HubTrial
	for rows.Next() {
		var t HubTrial
		if err := rows.Scan(&t.ID, &t.JobID, &t.TrialName, &t.TaskName, &t.TaskContentHash, &t.Lock, &t.AgentID, &t.Config, &t.Rewards, &t.ExceptionType, &t.EnvironmentSetup, &t.AgentSetup, &t.AgentExecution, &t.Verifier, &t.ArchivePath, &t.TrajectoryPath); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func InsertHubTrial(ctx context.Context, tx *sql.Tx, t HubTrial) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO hub_trial (id, job_id, trial_name, task_name, task_content_hash, lock, agent_id, config, rewards, exception_type, environment_setup, agent_setup, agent_execution, verifier, archive_path, trajectory_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.JobID, t.TrialName, t.TaskName, t.TaskContentHash, t.Lock, t.AgentID, t.Config, t.Rewards, t.ExceptionType, t.EnvironmentSetup, t.AgentSetup, t.AgentExecution, t.Verifier, t.ArchivePath, t.TrajectoryPath)
	return err
}

func PatchHubTrial(ctx context.Context, tx *sql.Tx, id string, fields map[string]any) (int64, error) {
	if len(fields) == 0 {
		return 0, nil
	}
	var sets []string
	var args []any
	for k, v := range fields {
		sets = append(sets, k+" = ?")
		args = append(args, v)
	}
	args = append(args, id)
	res, err := tx.ExecContext(ctx, "UPDATE hub_trial SET "+strings.Join(sets, ", ")+" WHERE id = ?", args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func GetHubAgent(ctx context.Context, q queryer, addedBy, name, version string) (*HubAgent, error) {
	row := q.QueryRowContext(ctx, `SELECT id, added_by, name, version FROM hub_agent WHERE added_by = ? AND name = ? AND version = ?`, addedBy, name, version)
	var a HubAgent
	err := row.Scan(&a.ID, &a.AddedBy, &a.Name, &a.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func InsertHubAgent(ctx context.Context, tx *sql.Tx, a HubAgent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO hub_agent (id, added_by, name, version) VALUES (?, ?, ?, ?)`, a.ID, a.AddedBy, a.Name, a.Version)
	return err
}

func GetHubModel(ctx context.Context, q queryer, addedBy, name, provider string) (*HubModel, error) {
	row := q.QueryRowContext(ctx, `SELECT id, added_by, name, provider FROM hub_model WHERE added_by = ? AND name = ? AND provider = ?`, addedBy, name, provider)
	var m HubModel
	err := row.Scan(&m.ID, &m.AddedBy, &m.Name, &m.Provider)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func InsertHubModel(ctx context.Context, tx *sql.Tx, m HubModel) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO hub_model (id, added_by, name, provider) VALUES (?, ?, ?, ?)`, m.ID, m.AddedBy, m.Name, m.Provider)
	return err
}

func InsertHubTrialModelIgnore(ctx context.Context, tx *sql.Tx, trialID, modelID string, inTok, cacheTok, outTok *int64, cost *float64) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO hub_trial_model (trial_id, model_id, n_input_tokens, n_cache_tokens, n_output_tokens, cost_usd) VALUES (?, ?, ?, ?, ?, ?)`,
		trialID, modelID, inTok, cacheTok, outTok, cost)
	return err
}

func InsertFinalized(ctx context.Context, tx *sql.Tx, job AnalysisJob, trials []AnalysisTrial, ov Overlay) error {
	if ov.AttestationStatus == "" {
		ov.AttestationStatus = "unsigned"
	}
	if ov.Extra == "" {
		ov.Extra = "{}"
	}
	applyAgentIdentity(&job, MajorityAgentIdentity(trials))
	_, err := tx.ExecContext(ctx, `INSERT INTO jobs (
		job_id, job_name, s3_prefix, started_at, finished_at, n_trials, n_errors, n_retries, n_reward_1, pass_at_1,
		n_input_tokens, n_cache_tokens, n_output_tokens, cost_usd, n_agent_steps, usage_reported, ingest_sha256, ingested_at,
		agent_name, agent_version, model_name, model_provider
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.JobID, job.JobName, job.S3Prefix, job.StartedAt, job.FinishedAt, job.NTrials, job.NErrors, job.NRetries, job.NReward1, job.PassAt1,
		job.NInputTokens, job.NCacheTokens, job.NOutputTokens, job.CostUSD, job.NAgentSteps, boolToInt(job.UsageReported), job.IngestSHA256, job.IngestedAt,
		job.AgentName, job.AgentVersion, job.ModelName, job.ModelProvider)
	if err != nil {
		return err
	}
	for _, t := range trials {
		_, err := tx.ExecContext(ctx, `INSERT INTO trials (
			trial_id, job_id, task_name, task_checksum, trial_name, task_id, source, trial_uri,
			agent_name, agent_version, model_name, model_provider, agent_info, reward, f2p, p2p, verifier_rewards,
			exception_type, exception_info, started_at, finished_at, environment_setup, agent_setup, agent_execution, verifier_timing,
			n_input_tokens, n_cache_tokens, n_output_tokens, cost_usd, n_agent_steps, usage_reported, trajectory_uri, s3_trial_prefix
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.TrialID, t.JobID, t.TaskName, t.TaskChecksum, t.TrialName, t.TaskID, t.Source, t.TrialURI,
			t.AgentName, t.AgentVersion, t.ModelName, t.ModelProvider, t.AgentInfo, t.Reward, t.F2P, t.P2P, t.VerifierRewards,
			t.ExceptionType, t.ExceptionInfo, t.StartedAt, t.FinishedAt, t.EnvironmentSetup, t.AgentSetup, t.AgentExecution, t.VerifierTiming,
			t.NInputTokens, t.NCacheTokens, t.NOutputTokens, t.CostUSD, t.NAgentSteps, boolToInt(t.UsageReported), t.TrajectoryURI, t.S3TrialPrefix)
		if err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO job_overlays (
		job_id, runner_name, runner_version, sandbox_type, sandbox_location, endpoint_class, job_type, job_type_reason,
		attestation_status, attestor, hub_org, harbor_version, dataset_name, dataset_version, dataset_ref, dataset_path, incomparability, extra, listed
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`,
		ov.JobID, ov.RunnerName, ov.RunnerVersion, ov.SandboxType, ov.SandboxLocation, ov.EndpointClass, ov.JobType, ov.JobTypeReason,
		ov.AttestationStatus, ov.Attestor, ov.HubOrg, ov.HarborVersion, ov.DatasetName, ov.DatasetVersion, ov.DatasetRef, ov.DatasetPath, ov.Incomparability, ov.Extra)
	return err
}

const analysisJobSelect = `job_id, job_name, s3_prefix, started_at, finished_at, n_trials, n_errors, n_retries, n_reward_1, pass_at_1,
		n_input_tokens, n_cache_tokens, n_output_tokens, cost_usd, n_agent_steps, usage_reported, ingest_sha256, ingested_at,
		agent_name, agent_version, model_name, model_provider`

func scanAnalysisJob(row rowScanner) (*AnalysisJob, error) {
	var j AnalysisJob
	var reported int
	err := row.Scan(&j.JobID, &j.JobName, &j.S3Prefix, &j.StartedAt, &j.FinishedAt, &j.NTrials, &j.NErrors, &j.NRetries, &j.NReward1, &j.PassAt1,
		&j.NInputTokens, &j.NCacheTokens, &j.NOutputTokens, &j.CostUSD, &j.NAgentSteps, &reported, &j.IngestSHA256, &j.IngestedAt,
		&j.AgentName, &j.AgentVersion, &j.ModelName, &j.ModelProvider)
	if err != nil {
		return nil, err
	}
	j.UsageReported = reported != 0
	return &j, nil
}

func GetAnalysisJob(ctx context.Context, q queryer, jobID string) (*AnalysisJob, error) {
	row := q.QueryRowContext(ctx, `SELECT `+analysisJobSelect+` FROM jobs WHERE job_id = ?`, jobID)
	j, err := scanAnalysisJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}

func (s *Store) GetAnalysisJob(ctx context.Context, jobID string) (*AnalysisJob, error) {
	return GetAnalysisJob(ctx, s.DB, jobID)
}

func (s *Store) GetOverlay(ctx context.Context, jobID string) (*Overlay, error) {
	row := s.DB.QueryRowContext(ctx, `SELECT job_id, runner_name, runner_version, sandbox_type, sandbox_location, endpoint_class, job_type, job_type_reason,
		attestation_status, attestor, hub_org, harbor_version, dataset_name, dataset_version, dataset_ref, dataset_path, incomparability, extra, listed
		FROM job_overlays WHERE job_id = ?`, jobID)
	var o Overlay
	var listed int
	err := row.Scan(&o.JobID, &o.RunnerName, &o.RunnerVersion, &o.SandboxType, &o.SandboxLocation, &o.EndpointClass, &o.JobType, &o.JobTypeReason,
		&o.AttestationStatus, &o.Attestor, &o.HubOrg, &o.HarborVersion, &o.DatasetName, &o.DatasetVersion, &o.DatasetRef, &o.DatasetPath, &o.Incomparability, &o.Extra, &listed)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	o.Listed = listed != 0
	return &o, nil
}

func UpdateOverlay(ctx context.Context, tx *sql.Tx, o Overlay) error {
	if o.AttestationStatus == "" {
		o.AttestationStatus = "unsigned"
	}
	if o.Extra == "" {
		o.Extra = "{}"
	}
	res, err := tx.ExecContext(ctx, `UPDATE job_overlays SET
		runner_name=?, runner_version=?, sandbox_type=?, sandbox_location=?, endpoint_class=?, job_type=?, job_type_reason=?,
		attestation_status=?, attestor=?, hub_org=?, harbor_version=?, dataset_name=?, dataset_version=?, dataset_ref=?, dataset_path=?, incomparability=?, extra=?, listed=?
		WHERE job_id=?`,
		o.RunnerName, o.RunnerVersion, o.SandboxType, o.SandboxLocation, o.EndpointClass, o.JobType, o.JobTypeReason,
		o.AttestationStatus, o.Attestor, o.HubOrg, o.HarborVersion, o.DatasetName, o.DatasetVersion, o.DatasetRef, o.DatasetPath, o.Incomparability, o.Extra, boolToInt(o.Listed), o.JobID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type ListJobsFilter struct {
	JobName           *string
	AgentName         *string
	ModelName         *string
	JobType           *string // pointer to empty string means IS NULL
	AttestationStatus *string
	ListedOnly        bool
	Limit             int
	Offset            int
}

type JobRow struct {
	AnalysisJob
	Overlay Overlay
}

func (s *Store) ListJobs(ctx context.Context, f ListJobsFilter) ([]JobRow, int, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	if f.Limit > 200 {
		f.Limit = 200
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := []string{"1=1"}
	args := []any{}
	if f.JobName != nil {
		where = append(where, "j.job_name = ?")
		args = append(args, *f.JobName)
	}
	if f.JobType != nil {
		if *f.JobType == "" {
			where = append(where, "o.job_type IS NULL")
		} else {
			where = append(where, "o.job_type = ?")
			args = append(args, *f.JobType)
		}
	}
	if f.AttestationStatus != nil {
		where = append(where, "o.attestation_status = ?")
		args = append(args, *f.AttestationStatus)
	}
	if f.ListedOnly {
		where = append(where, "COALESCE(o.listed, 1) = 1")
	}
	if f.AgentName != nil {
		where = append(where, "j.agent_name = ?")
		args = append(args, *f.AgentName)
	}
	if f.ModelName != nil {
		where = append(where, "j.model_name = ?")
		args = append(args, *f.ModelName)
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM jobs j LEFT JOIN job_overlays o ON o.job_id = j.job_id WHERE "+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT j.job_id, j.job_name, j.s3_prefix, j.started_at, j.finished_at, j.n_trials, j.n_errors, j.n_retries, j.n_reward_1, j.pass_at_1,
		j.n_input_tokens, j.n_cache_tokens, j.n_output_tokens, j.cost_usd, j.n_agent_steps, j.usage_reported, j.ingest_sha256, j.ingested_at,
		j.agent_name, j.agent_version, j.model_name, j.model_provider,
		o.job_id, o.runner_name, o.runner_version, o.sandbox_type, o.sandbox_location, o.endpoint_class, o.job_type, o.job_type_reason,
		o.attestation_status, o.attestor, o.hub_org, o.harbor_version, o.dataset_name, o.dataset_version, o.dataset_ref, o.dataset_path, o.incomparability, o.extra, o.listed
		FROM jobs j LEFT JOIN job_overlays o ON o.job_id = j.job_id WHERE ` + w + ` ORDER BY j.ingested_at DESC LIMIT ? OFFSET ?`
	args2 := append(append([]any{}, args...), f.Limit, f.Offset)
	rows, err := s.DB.QueryContext(ctx, q, args2...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []JobRow
	for rows.Next() {
		var r JobRow
		var reported int
		var ovJob sql.NullString
		var listed sql.NullInt64
		if err := rows.Scan(
			&r.JobID, &r.JobName, &r.S3Prefix, &r.StartedAt, &r.FinishedAt, &r.NTrials, &r.NErrors, &r.NRetries, &r.NReward1, &r.PassAt1,
			&r.NInputTokens, &r.NCacheTokens, &r.NOutputTokens, &r.CostUSD, &r.NAgentSteps, &reported, &r.IngestSHA256, &r.IngestedAt,
			&r.AgentName, &r.AgentVersion, &r.ModelName, &r.ModelProvider,
			&ovJob, &r.Overlay.RunnerName, &r.Overlay.RunnerVersion, &r.Overlay.SandboxType, &r.Overlay.SandboxLocation, &r.Overlay.EndpointClass, &r.Overlay.JobType, &r.Overlay.JobTypeReason,
			&r.Overlay.AttestationStatus, &r.Overlay.Attestor, &r.Overlay.HubOrg, &r.Overlay.HarborVersion, &r.Overlay.DatasetName, &r.Overlay.DatasetVersion, &r.Overlay.DatasetRef, &r.Overlay.DatasetPath, &r.Overlay.Incomparability, &r.Overlay.Extra, &listed,
		); err != nil {
			return nil, 0, err
		}
		r.UsageReported = reported != 0
		if ovJob.Valid {
			r.Overlay.JobID = ovJob.String
		}
		r.Overlay.Listed = !listed.Valid || listed.Int64 != 0
		if r.Overlay.Extra == "" {
			r.Overlay.Extra = "{}"
		}
		if r.Overlay.AttestationStatus == "" {
			r.Overlay.AttestationStatus = "unsigned"
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

type ListTrialsFilter struct {
	JobID    string
	Reward   *float64
	TaskName *string
	Order    string
	Limit    int
	Offset   int
}

func (s *Store) ListTrials(ctx context.Context, f ListTrialsFilter) ([]AnalysisTrial, int, error) {
	s.noteTrialRead()
	if f.Limit <= 0 {
		f.Limit = 100
	}
	if f.Limit > 500 {
		f.Limit = 500
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	where := []string{"job_id = ?"}
	args := []any{f.JobID}
	if f.Reward != nil {
		where = append(where, "reward = ?")
		args = append(args, *f.Reward)
	}
	if f.TaskName != nil && *f.TaskName != "" {
		where = append(where, "task_name LIKE ?")
		args = append(args, "%"+*f.TaskName+"%")
	}
	order := "task_name ASC"
	switch f.Order {
	case "started_at":
		order = "started_at ASC"
	case "reward":
		order = "reward DESC, task_name ASC"
	case "task_name", "":
		order = "task_name ASC"
	default:
		order = "task_name ASC"
	}
	w := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM trials WHERE "+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT trial_id, job_id, task_name, task_checksum, trial_name, task_id, source, trial_uri,
		agent_name, agent_version, model_name, model_provider, agent_info, reward, f2p, p2p, verifier_rewards,
		exception_type, exception_info, started_at, finished_at, environment_setup, agent_setup, agent_execution, verifier_timing,
		n_input_tokens, n_cache_tokens, n_output_tokens, cost_usd, n_agent_steps, usage_reported, trajectory_uri, s3_trial_prefix
		FROM trials WHERE ` + w + ` ORDER BY ` + order + ` LIMIT ? OFFSET ?`
	args2 := append(append([]any{}, args...), f.Limit, f.Offset)
	rows, err := s.DB.QueryContext(ctx, q, args2...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out, err := scanTrials(rows)
	return out, total, err
}

func (s *Store) GetTrial(ctx context.Context, jobID, trialID string) (*AnalysisTrial, error) {
	s.noteTrialRead()
	row := s.DB.QueryRowContext(ctx, `SELECT trial_id, job_id, task_name, task_checksum, trial_name, task_id, source, trial_uri,
		agent_name, agent_version, model_name, model_provider, agent_info, reward, f2p, p2p, verifier_rewards,
		exception_type, exception_info, started_at, finished_at, environment_setup, agent_setup, agent_execution, verifier_timing,
		n_input_tokens, n_cache_tokens, n_output_tokens, cost_usd, n_agent_steps, usage_reported, trajectory_uri, s3_trial_prefix
		FROM trials WHERE job_id = ? AND trial_id = ?`, jobID, trialID)
	t, err := scanTrial(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return t, err
}

func (s *Store) AllTrials(ctx context.Context, jobID string) ([]AnalysisTrial, error) {
	s.noteTrialRead()
	rows, err := s.DB.QueryContext(ctx, `SELECT trial_id, job_id, task_name, task_checksum, trial_name, task_id, source, trial_uri,
		agent_name, agent_version, model_name, model_provider, agent_info, reward, f2p, p2p, verifier_rewards,
		exception_type, exception_info, started_at, finished_at, environment_setup, agent_setup, agent_execution, verifier_timing,
		n_input_tokens, n_cache_tokens, n_output_tokens, cost_usd, n_agent_steps, usage_reported, trajectory_uri, s3_trial_prefix
		FROM trials WHERE job_id = ?`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTrials(rows)
}

func scanTrials(rows *sql.Rows) ([]AnalysisTrial, error) {
	var out []AnalysisTrial
	for rows.Next() {
		t, err := scanTrial(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanTrial(row rowScanner) (*AnalysisTrial, error) {
	var t AnalysisTrial
	var reported int
	err := row.Scan(&t.TrialID, &t.JobID, &t.TaskName, &t.TaskChecksum, &t.TrialName, &t.TaskID, &t.Source, &t.TrialURI,
		&t.AgentName, &t.AgentVersion, &t.ModelName, &t.ModelProvider, &t.AgentInfo, &t.Reward, &t.F2P, &t.P2P, &t.VerifierRewards,
		&t.ExceptionType, &t.ExceptionInfo, &t.StartedAt, &t.FinishedAt, &t.EnvironmentSetup, &t.AgentSetup, &t.AgentExecution, &t.VerifierTiming,
		&t.NInputTokens, &t.NCacheTokens, &t.NOutputTokens, &t.CostUSD, &t.NAgentSteps, &reported, &t.TrajectoryURI, &t.S3TrialPrefix)
	if err != nil {
		return nil, err
	}
	t.UsageReported = reported != 0
	return &t, nil
}

func NowRFC3339() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func TextJSON(v any) *string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		return &t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			s := fmt.Sprint(t)
			return &s
		}
		s := string(b)
		return &s
	}
}

type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func IsUniqueErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique constraint") || strings.Contains(msg, "constraint failed")
}

func HubJobMap(j *HubJob, selectCols []string) map[string]any {
	m := map[string]any{
		"id":               j.ID,
		"job_name":         j.JobName,
		"config":           parseMaybeJSON(j.Config),
		"visibility":       j.Visibility,
		"started_at":       j.StartedAt,
		"finished_at":      j.FinishedAt,
		"archive_path":     j.ArchivePath,
		"log_path":         j.LogPath,
		"n_planned_trials": j.NPlannedTrials,
		"org_id":           j.OrgID,
		"created_by":       j.CreatedBy,
		"is_hosted":        j.IsHosted,
	}
	return project(m, selectCols)
}

func HubTrialMap(t *HubTrial, selectCols []string) map[string]any {
	m := map[string]any{
		"id":                t.ID,
		"job_id":            t.JobID,
		"trial_name":        t.TrialName,
		"task_name":         t.TaskName,
		"task_content_hash": t.TaskContentHash,
		"lock":              parseMaybeJSON(t.Lock),
		"agent_id":          t.AgentID,
		"config":            parseMaybeJSON(t.Config),
		"rewards":           parseMaybeJSON(t.Rewards),
		"exception_type":    t.ExceptionType,
		"environment_setup": parseMaybeJSON(t.EnvironmentSetup),
		"agent_setup":       parseMaybeJSON(t.AgentSetup),
		"agent_execution":   parseMaybeJSON(t.AgentExecution),
		"verifier":          parseMaybeJSON(t.Verifier),
		"archive_path":      t.ArchivePath,
		"trajectory_path":   t.TrajectoryPath,
	}
	return project(m, selectCols)
}

func parseMaybeJSON(s *string) any {
	if s == nil {
		return nil
	}
	raw := strings.TrimSpace(*s)
	if raw == "" {
		return nil
	}
	if raw[0] == '{' || raw[0] == '[' {
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			return v
		}
	}
	return *s
}

func project(m map[string]any, cols []string) map[string]any {
	if len(cols) == 0 {
		return m
	}
	out := map[string]any{}
	for _, c := range cols {
		c = strings.TrimSpace(c)
		if c == "*" || c == "" {
			return m
		}
		if v, ok := m[c]; ok {
			out[c] = v
		}
	}
	return out
}

type ViewerStats struct {
	NJobs                int
	NTrials              int
	MeanPassAt1          *float64
	NUsageReportedJobs   int
	NUsageUnreportedJobs int
}

func (s *Store) ViewerStats(ctx context.Context) (ViewerStats, error) {
	var st ViewerStats
	var mean sql.NullFloat64
	err := s.DB.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(SUM(j.n_trials), 0), AVG(j.pass_at_1),
			COALESCE(SUM(CASE WHEN j.usage_reported = 1 THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN j.usage_reported = 0 THEN 1 ELSE 0 END), 0)
		FROM jobs j
		LEFT JOIN job_overlays o ON o.job_id = j.job_id
		WHERE COALESCE(o.listed, 1) = 1`).Scan(
		&st.NJobs, &st.NTrials, &mean, &st.NUsageReportedJobs, &st.NUsageUnreportedJobs)
	if err != nil {
		return ViewerStats{}, err
	}
	if mean.Valid && st.NJobs > 0 {
		v := mean.Float64
		st.MeanPassAt1 = &v
	}
	return st, nil
}

type AdminJobRow struct {
	JobID             string
	JobName           string
	ArchivePath       *string
	StartedAt         *string
	FinishedAt        *string
	NPlannedTrials    *int64
	NHubTrials        int
	Finalized         bool
	NTrials           int
	NReward1          int
	PassAt1           *float64
	NInputTokens      int64
	NCacheTokens      int64
	NOutputTokens     int64
	CostUSD           float64
	NAgentSteps       int64
	UsageReported     bool
	IngestedAt        *string
	S3Prefix          *string
	AttestationStatus string
	Attestor          *string
	EndpointClass     *string
	JobType           *string
	JobTypeReason     *string
	Incomparability   *string
	Listed            bool
	DatasetRef        *string
	DatasetPath       *string
	RunnerName        *string
	RunnerVersion     *string
}

func (s *Store) ListAdminJobs(ctx context.Context, limit, offset int) ([]AdminJobRow, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM hub_job`).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT h.id, COALESCE(h.job_name, j.job_name, ''), h.archive_path, h.started_at, h.finished_at, h.n_planned_trials,
		(SELECT COUNT(*) FROM hub_trial ht WHERE ht.job_id = h.id),
		j.job_id, j.n_trials, j.n_reward_1, j.pass_at_1, j.n_input_tokens, j.n_cache_tokens, j.n_output_tokens, j.cost_usd, j.n_agent_steps, j.usage_reported, j.ingested_at, j.s3_prefix,
		o.attestation_status, o.attestor, o.endpoint_class, o.job_type, o.job_type_reason, o.incomparability, o.listed, o.dataset_ref, o.dataset_path, o.runner_name, o.runner_version
		FROM hub_job h
		LEFT JOIN jobs j ON j.job_id = h.id
		LEFT JOIN job_overlays o ON o.job_id = h.id
		ORDER BY COALESCE(j.ingested_at, h.finished_at, h.started_at, h.id) DESC
		LIMIT ? OFFSET ?`
	rows, err := s.DB.QueryContext(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []AdminJobRow
	for rows.Next() {
		r, err := scanAdminJob(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}

func (s *Store) GetAdminJob(ctx context.Context, jobID string) (*AdminJobRow, error) {
	q := `SELECT h.id, COALESCE(h.job_name, j.job_name, ''), h.archive_path, h.started_at, h.finished_at, h.n_planned_trials,
		(SELECT COUNT(*) FROM hub_trial ht WHERE ht.job_id = h.id),
		j.job_id, j.n_trials, j.n_reward_1, j.pass_at_1, j.n_input_tokens, j.n_cache_tokens, j.n_output_tokens, j.cost_usd, j.n_agent_steps, j.usage_reported, j.ingested_at, j.s3_prefix,
		o.attestation_status, o.attestor, o.endpoint_class, o.job_type, o.job_type_reason, o.incomparability, o.listed, o.dataset_ref, o.dataset_path, o.runner_name, o.runner_version
		FROM hub_job h
		LEFT JOIN jobs j ON j.job_id = h.id
		LEFT JOIN job_overlays o ON o.job_id = h.id
		WHERE h.id = ?`
	row := s.DB.QueryRowContext(ctx, q, jobID)
	r, err := scanAdminJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

type adminRowScanner interface {
	Scan(dest ...any) error
}

func scanAdminJob(row adminRowScanner) (AdminJobRow, error) {
	var r AdminJobRow
	var analysisID sql.NullString
	var nTrials, nReward1, reported sql.NullInt64
	var pass sql.NullFloat64
	var inTok, cacheTok, outTok, steps sql.NullInt64
	var cost sql.NullFloat64
	var ingested, s3prefix sql.NullString
	var attest sql.NullString
	var listed sql.NullInt64
	err := row.Scan(
		&r.JobID, &r.JobName, &r.ArchivePath, &r.StartedAt, &r.FinishedAt, &r.NPlannedTrials, &r.NHubTrials,
		&analysisID, &nTrials, &nReward1, &pass, &inTok, &cacheTok, &outTok, &cost, &steps, &reported, &ingested, &s3prefix,
		&attest, &r.Attestor, &r.EndpointClass, &r.JobType, &r.JobTypeReason, &r.Incomparability, &listed, &r.DatasetRef, &r.DatasetPath, &r.RunnerName, &r.RunnerVersion,
	)
	if err != nil {
		return r, err
	}
	r.Finalized = analysisID.Valid
	if nTrials.Valid {
		r.NTrials = int(nTrials.Int64)
	}
	if nReward1.Valid {
		r.NReward1 = int(nReward1.Int64)
	}
	if pass.Valid {
		v := pass.Float64
		r.PassAt1 = &v
	}
	if inTok.Valid {
		r.NInputTokens = inTok.Int64
	}
	if cacheTok.Valid {
		r.NCacheTokens = cacheTok.Int64
	}
	if outTok.Valid {
		r.NOutputTokens = outTok.Int64
	}
	if cost.Valid {
		r.CostUSD = cost.Float64
	}
	if steps.Valid {
		r.NAgentSteps = steps.Int64
	}
	r.UsageReported = reported.Valid && reported.Int64 != 0
	if ingested.Valid {
		r.IngestedAt = &ingested.String
	}
	if s3prefix.Valid {
		r.S3Prefix = &s3prefix.String
	}
	if attest.Valid && attest.String != "" {
		r.AttestationStatus = attest.String
	} else if r.Finalized {
		r.AttestationStatus = "unsigned"
	}
	r.Listed = !listed.Valid || listed.Int64 != 0
	if !r.Finalized {
		r.Listed = true
	}
	return r, nil
}

type DeleteJobResult struct {
	Prefixes []string
}

func (s *Store) DeleteJob(ctx context.Context, jobID string) (*DeleteJobResult, error) {
	hub, hubErr := GetHubJob(ctx, s.DB, jobID)
	aj, ajErr := s.GetAnalysisJob(ctx, jobID)
	if errors.Is(hubErr, ErrNotFound) && errors.Is(ajErr, ErrNotFound) {
		return nil, ErrNotFound
	}
	if hubErr != nil && !errors.Is(hubErr, ErrNotFound) {
		return nil, hubErr
	}
	if ajErr != nil && !errors.Is(ajErr, ErrNotFound) {
		return nil, ajErr
	}
	prefixes := map[string]struct{}{}
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p != "" {
			prefixes[p] = struct{}{}
		}
	}
	add("jobs/" + jobID + "/")
	if aj != nil {
		add(aj.S3Prefix)
		trials, err := s.AllTrials(ctx, jobID)
		if err != nil {
			return nil, err
		}
		for _, t := range trials {
			add("trials/" + t.TrialID + "/")
			if t.S3TrialPrefix != nil {
				add(*t.S3TrialPrefix)
			}
		}
	}
	if hub != nil {
		if hub.ArchivePath != nil {
			add(*hub.ArchivePath)
		}
		ht, err := ListHubTrialsByJob(ctx, s.DB, jobID, 0, 100000)
		if err != nil {
			return nil, err
		}
		for _, t := range ht {
			add("trials/" + t.ID + "/")
			if t.ArchivePath != nil {
				add(*t.ArchivePath)
			}
		}
	}
	err := s.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM hub_trial_model WHERE trial_id IN (SELECT id FROM hub_trial WHERE job_id = ?)`, jobID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM hub_trial WHERE job_id = ?`, jobID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM jobs WHERE job_id = ?`, jobID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM hub_job WHERE id = ?`, jobID); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := &DeleteJobResult{}
	for p := range prefixes {
		out.Prefixes = append(out.Prefixes, p)
	}
	sort.Strings(out.Prefixes)
	return out, nil
}

func (s *Store) CountHubJobs(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM hub_job`).Scan(&n)
	return n, err
}

func (s *Store) CountFinalizedJobs(ctx context.Context) (int, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs`).Scan(&n)
	return n, err
}
