package query

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"

	"evo-harness/internal/evaldisplay/ingest"
	"evo-harness/internal/evaldisplay/store"
)

type Service struct {
	Store *store.Store
}

func New(st *store.Store) *Service { return &Service{Store: st} }

type AgentModel struct {
	AgentName     string  `json:"agent_name"`
	AgentVersion  *string `json:"agent_version"`
	ModelName     *string `json:"model_name"`
	ModelProvider *string `json:"model_provider"`
	Consistent    bool    `json:"agent_model_consistent"`
}

type JobListItem struct {
	JobID             string   `json:"job_id"`
	JobName           string   `json:"job_name"`
	AgentName         string   `json:"agent_name"`
	AgentVersion      *string  `json:"agent_version"`
	ModelName         *string  `json:"model_name"`
	ModelProvider     *string  `json:"model_provider"`
	NTrials           int      `json:"n_trials"`
	NReward1          int      `json:"n_reward_1"`
	PassAt1           float64  `json:"pass_at_1"`
	NInputTokens      int64    `json:"n_input_tokens"`
	NCacheTokens      int64    `json:"n_cache_tokens"`
	NOutputTokens     int64    `json:"n_output_tokens"`
	CostUSD           float64  `json:"cost_usd"`
	NAgentSteps       int64    `json:"n_agent_steps"`
	UsageReported     bool     `json:"usage_reported"`
	StartedAt         *string  `json:"started_at"`
	FinishedAt        *string  `json:"finished_at"`
	DurationSec       *float64 `json:"duration_sec"`
	JobType           *string  `json:"job_type"`
	EndpointClass     *string  `json:"endpoint_class"`
	AttestationStatus string   `json:"attestation_status"`
	Incomparability   *string  `json:"incomparability"`
	RunnerName        *string  `json:"runner_name"`
	RunnerVersion     *string  `json:"runner_version"`
	Listed            bool     `json:"listed"`
}

type JobDetail struct {
	JobListItem
	HubOrg          *string         `json:"hub_org"`
	HarborVersion   *string         `json:"harbor_version"`
	DatasetPath     *string         `json:"dataset_path"`
	DatasetRef      *string         `json:"dataset_ref"`
	DatasetName     *string         `json:"dataset_name"`
	SandboxType     *string         `json:"sandbox_type"`
	SandboxLocation *string         `json:"sandbox_location"`
	JobTypeReason   *string         `json:"job_type_reason"`
	Attestor        *string         `json:"attestor"`
	NErrors         int             `json:"n_errors"`
	NRetries        int             `json:"n_retries"`
	HarborConfig    json.RawMessage `json:"harbor_config,omitempty"`
	HarborResult    json.RawMessage `json:"harbor_result,omitempty"`
}

type TrialListItem struct {
	TrialID       string   `json:"trial_id"`
	TaskName      string   `json:"task_name"`
	TaskChecksum  string   `json:"task_checksum"`
	AgentName     string   `json:"agent_name"`
	ModelName     *string  `json:"model_name"`
	Reward        float64  `json:"reward"`
	F2P           *float64 `json:"f2p"`
	P2P           *float64 `json:"p2p"`
	ExceptionType *string  `json:"exception_type"`
	StartedAt     *string  `json:"started_at"`
	FinishedAt    *string  `json:"finished_at"`
	DurationSec   *float64 `json:"duration_sec"`
	NInputTokens  int64    `json:"n_input_tokens"`
	NCacheTokens  int64    `json:"n_cache_tokens"`
	NOutputTokens int64    `json:"n_output_tokens"`
	CostUSD       float64  `json:"cost_usd"`
	NAgentSteps   int64    `json:"n_agent_steps"`
	UsageReported bool     `json:"usage_reported"`
}

type TrialDetail struct {
	TrialListItem
	AgentInfo        json.RawMessage `json:"agent_info"`
	VerifierRewards  json.RawMessage `json:"verifier_rewards"`
	ExceptionInfo    json.RawMessage `json:"exception_info"`
	EnvironmentSetup json.RawMessage `json:"environment_setup"`
	AgentSetup       json.RawMessage `json:"agent_setup"`
	AgentExecution   json.RawMessage `json:"agent_execution"`
	VerifierTiming   json.RawMessage `json:"verifier"`
	TrajectoryURI    *string         `json:"trajectory_uri"`
	HarborConfig     json.RawMessage `json:"harbor_config,omitempty"`
	HarborResult     json.RawMessage `json:"harbor_result,omitempty"`
}

type DurationStats struct {
	N    int      `json:"n"`
	Min  *float64 `json:"min"`
	P50  *float64 `json:"p50"`
	Mean *float64 `json:"mean"`
	Max  *float64 `json:"max"`
}

type Aggregate struct {
	JobID                string                   `json:"job_id"`
	JobName              string                   `json:"job_name"`
	AgentName            string                   `json:"agent_name"`
	AgentVersion         *string                  `json:"agent_version"`
	ModelName            *string                  `json:"model_name"`
	NTrials              int                      `json:"n_trials"`
	NReward1             int                      `json:"n_reward_1"`
	NReward0             int                      `json:"n_reward_0"`
	PassAt1              float64                  `json:"pass_at_1"`
	PassAt1Fraction      string                   `json:"pass_at_1_fraction"`
	NInputTokens         int64                    `json:"n_input_tokens"`
	NCacheTokens         int64                    `json:"n_cache_tokens"`
	NOutputTokens        int64                    `json:"n_output_tokens"`
	CostUSD              float64                  `json:"cost_usd"`
	NAgentSteps          int64                    `json:"n_agent_steps"`
	UsageReported        bool                     `json:"usage_reported"`
	NUsageReportedTrials int                      `json:"n_usage_reported_trials"`
	DurationSec          DurationStats            `json:"duration_sec"`
	PhaseDurationSec     map[string]DurationStats `json:"phase_duration_sec,omitempty"`
	AgentModelConsistent bool                     `json:"agent_model_consistent"`
}

type CompareResult struct {
	DatasetMatch bool      `json:"dataset_match"`
	OverlapN     int       `json:"overlap_n"`
	JobA         Aggregate `json:"job_a"`
	JobB         Aggregate `json:"job_b"`
}

func (s *Service) ListJobs(ctx context.Context, f store.ListJobsFilter) ([]JobListItem, int, error) {
	f.ListedOnly = true
	rows, total, err := s.Store.ListJobs(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]JobListItem, 0, len(rows))
	for _, r := range rows {
		items = append(items, jobListItem(r))
	}
	return items, total, nil
}

func (s *Service) GetJob(ctx context.Context, jobID string) (*JobDetail, error) {
	j, err := s.Store.GetAnalysisJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	ov, err := s.Store.GetOverlay(ctx, jobID)
	if err != nil && err != store.ErrNotFound {
		return nil, err
	}
	if ov == nil {
		ov = &store.Overlay{JobID: jobID, AttestationStatus: "unsigned", Extra: "{}", Listed: true}
	}
	if !ov.Listed {
		return nil, store.ErrNotFound
	}
	row := store.JobRow{AnalysisJob: *j, Overlay: *ov}
	item := jobListItem(row)
	d := &JobDetail{
		JobListItem:     item,
		HubOrg:          ov.HubOrg,
		HarborVersion:   ov.HarborVersion,
		DatasetPath:     ov.DatasetPath,
		DatasetRef:      ov.DatasetRef,
		DatasetName:     ov.DatasetName,
		SandboxType:     ov.SandboxType,
		SandboxLocation: ov.SandboxLocation,
		JobTypeReason:   ov.JobTypeReason,
		Attestor:        ov.Attestor,
		NErrors:         j.NErrors,
		NRetries:        j.NRetries,
	}
	return d, nil
}

func (s *Service) ListTrials(ctx context.Context, f store.ListTrialsFilter) ([]TrialListItem, int, error) {
	if err := s.requireListed(ctx, f.JobID); err != nil {
		return nil, 0, err
	}
	rows, total, err := s.Store.ListTrials(ctx, f)
	if err != nil {
		return nil, 0, err
	}
	items := make([]TrialListItem, 0, len(rows))
	for _, t := range rows {
		items = append(items, trialListItem(t))
	}
	return items, total, nil
}

func (s *Service) GetTrial(ctx context.Context, jobID, trialID string) (*TrialDetail, error) {
	if err := s.requireListed(ctx, jobID); err != nil {
		return nil, err
	}
	t, err := s.Store.GetTrial(ctx, jobID, trialID)
	if err != nil {
		return nil, err
	}
	item := trialListItem(*t)
	d := &TrialDetail{
		TrialListItem:    item,
		AgentInfo:        json.RawMessage(orJSON(t.AgentInfo)),
		VerifierRewards:  json.RawMessage(orJSON(t.VerifierRewards)),
		ExceptionInfo:    rawOrNull(t.ExceptionInfo),
		EnvironmentSetup: rawOrNull(t.EnvironmentSetup),
		AgentSetup:       rawOrNull(t.AgentSetup),
		AgentExecution:   rawOrNull(t.AgentExecution),
		VerifierTiming:   rawOrNull(t.VerifierTiming),
		TrajectoryURI:    nil, // v1 never filled from peri.txt
	}
	return d, nil
}

func (s *Service) Aggregate(ctx context.Context, jobID string, withPhases bool) (*Aggregate, error) {
	if err := s.requireListed(ctx, jobID); err != nil {
		return nil, err
	}
	j, err := s.Store.GetAnalysisJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	trials, err := s.Store.AllTrials(ctx, jobID)
	if err != nil {
		return nil, err
	}
	am := majorityFromTrials(trials)
	nReported := 0
	var wall []float64
	phases := map[string][]float64{
		"environment_setup": nil,
		"agent_setup":       nil,
		"agent_execution":   nil,
		"verifier":          nil,
	}
	for _, t := range trials {
		if t.UsageReported {
			nReported++
		}
		if d := ingest.DurationSec(t.StartedAt, t.FinishedAt); d != nil {
			wall = append(wall, *d)
		}
		if withPhases {
			addPhase(phases, "environment_setup", t.EnvironmentSetup)
			addPhase(phases, "agent_setup", t.AgentSetup)
			addPhase(phases, "agent_execution", t.AgentExecution)
			addPhase(phases, "verifier", t.VerifierTiming)
		}
	}
	agg := &Aggregate{
		JobID:                j.JobID,
		JobName:              j.JobName,
		AgentName:            am.AgentName,
		AgentVersion:         am.AgentVersion,
		ModelName:            am.ModelName,
		NTrials:              j.NTrials,
		NReward1:             j.NReward1,
		NReward0:             j.NTrials - j.NReward1,
		PassAt1:              j.PassAt1,
		PassAt1Fraction:      formatFrac(j.NReward1, j.NTrials),
		NInputTokens:         j.NInputTokens,
		NCacheTokens:         j.NCacheTokens,
		NOutputTokens:        j.NOutputTokens,
		CostUSD:              j.CostUSD,
		NAgentSteps:          j.NAgentSteps,
		UsageReported:        j.UsageReported,
		NUsageReportedTrials: nReported,
		DurationSec:          statsOf(wall),
		AgentModelConsistent: am.Consistent,
	}
	if withPhases {
		agg.PhaseDurationSec = map[string]DurationStats{
			"environment_setup": statsOf(phases["environment_setup"]),
			"agent_setup":       statsOf(phases["agent_setup"]),
			"agent_execution":   statsOf(phases["agent_execution"]),
			"verifier":          statsOf(phases["verifier"]),
		}
	}
	return agg, nil
}

func (s *Service) Compare(ctx context.Context, jobA, jobB string) (*CompareResult, error) {
	if err := s.requireListed(ctx, jobA); err != nil {
		return nil, err
	}
	if err := s.requireListed(ctx, jobB); err != nil {
		return nil, err
	}
	a, err := s.Aggregate(ctx, jobA, false)
	if err != nil {
		return nil, err
	}
	b, err := s.Aggregate(ctx, jobB, false)
	if err != nil {
		return nil, err
	}
	oa, err := s.Store.GetOverlay(ctx, jobA)
	if err != nil && err != store.ErrNotFound {
		return nil, err
	}
	ob, err := s.Store.GetOverlay(ctx, jobB)
	if err != nil && err != store.ErrNotFound {
		return nil, err
	}
	match := false
	if oa != nil && ob != nil {
		if nonempty(oa.DatasetRef) && nonempty(ob.DatasetRef) && *oa.DatasetRef == *ob.DatasetRef {
			match = true
		} else if nonempty(oa.DatasetPath) && nonempty(ob.DatasetPath) && *oa.DatasetPath == *ob.DatasetPath {
			match = true
		}
	}
	ta, err := s.Store.AllTrials(ctx, jobA)
	if err != nil {
		return nil, err
	}
	tb, err := s.Store.AllTrials(ctx, jobB)
	if err != nil {
		return nil, err
	}
	set := map[string]struct{}{}
	for _, t := range ta {
		set[t.TaskChecksum] = struct{}{}
	}
	overlap := 0
	seen := map[string]struct{}{}
	for _, t := range tb {
		if _, ok := set[t.TaskChecksum]; ok {
			if _, dup := seen[t.TaskChecksum]; !dup {
				seen[t.TaskChecksum] = struct{}{}
				overlap++
			}
		}
	}
	return &CompareResult{DatasetMatch: match, OverlapN: overlap, JobA: *a, JobB: *b}, nil
}

func (s *Service) requireListed(ctx context.Context, jobID string) error {
	if _, err := s.Store.GetAnalysisJob(ctx, jobID); err != nil {
		return err
	}
	ov, err := s.Store.GetOverlay(ctx, jobID)
	if err == store.ErrNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	if ov != nil && !ov.Listed {
		return store.ErrNotFound
	}
	return nil
}

type Stats struct {
	NJobs                int      `json:"n_jobs"`
	NTrials              int      `json:"n_trials"`
	MeanPassAt1          *float64 `json:"mean_pass_at_1"`
	NUsageReportedJobs   int      `json:"n_usage_reported_jobs"`
	NUsageUnreportedJobs int      `json:"n_usage_unreported_jobs"`
}

func (s *Service) Stats(ctx context.Context) (*Stats, error) {
	st, err := s.Store.ViewerStats(ctx)
	if err != nil {
		return nil, err
	}
	return &Stats{
		NJobs:                st.NJobs,
		NTrials:              st.NTrials,
		MeanPassAt1:          st.MeanPassAt1,
		NUsageReportedJobs:   st.NUsageReportedJobs,
		NUsageUnreportedJobs: st.NUsageUnreportedJobs,
	}, nil
}

type AdminJob struct {
	JobID             string   `json:"job_id"`
	JobName           string   `json:"job_name"`
	IngestStatus      string   `json:"ingest_status"`
	Finalized         bool     `json:"finalized"`
	ArchivePath       *string  `json:"archive_path"`
	NPlannedTrials    *int64   `json:"n_planned_trials"`
	NHubTrials        int      `json:"n_hub_trials"`
	NTrials           int      `json:"n_trials"`
	NReward1          int      `json:"n_reward_1"`
	PassAt1           *float64 `json:"pass_at_1"`
	NInputTokens      int64    `json:"n_input_tokens"`
	NCacheTokens      int64    `json:"n_cache_tokens"`
	NOutputTokens     int64    `json:"n_output_tokens"`
	CostUSD           float64  `json:"cost_usd"`
	NAgentSteps       int64    `json:"n_agent_steps"`
	UsageReported     bool     `json:"usage_reported"`
	AttestationStatus string   `json:"attestation_status"`
	Attestor          *string  `json:"attestor"`
	EndpointClass     *string  `json:"endpoint_class"`
	JobType           *string  `json:"job_type"`
	JobTypeReason     *string  `json:"job_type_reason"`
	Incomparability   *string  `json:"incomparability"`
	Listed            bool     `json:"listed"`
	DatasetRef        *string  `json:"dataset_ref"`
	DatasetPath       *string  `json:"dataset_path"`
	RunnerName        *string  `json:"runner_name"`
	RunnerVersion     *string  `json:"runner_version"`
	StartedAt         *string  `json:"started_at"`
	FinishedAt        *string  `json:"finished_at"`
	IngestedAt        *string  `json:"ingested_at"`
}

func adminJobFrom(r store.AdminJobRow) AdminJob {
	status := "uploading"
	if r.Finalized {
		status = "finalized"
	} else if r.ArchivePath != nil && strings.TrimSpace(*r.ArchivePath) != "" {
		status = "incomplete"
	}
	return AdminJob{
		JobID: r.JobID, JobName: r.JobName, IngestStatus: status, Finalized: r.Finalized,
		ArchivePath: r.ArchivePath, NPlannedTrials: r.NPlannedTrials, NHubTrials: r.NHubTrials,
		NTrials: r.NTrials, NReward1: r.NReward1, PassAt1: r.PassAt1,
		NInputTokens: r.NInputTokens, NCacheTokens: r.NCacheTokens, NOutputTokens: r.NOutputTokens,
		CostUSD: r.CostUSD, NAgentSteps: r.NAgentSteps, UsageReported: r.UsageReported,
		AttestationStatus: r.AttestationStatus, Attestor: r.Attestor, EndpointClass: r.EndpointClass,
		JobType: r.JobType, JobTypeReason: r.JobTypeReason, Incomparability: r.Incomparability,
		Listed: r.Listed, DatasetRef: r.DatasetRef, DatasetPath: r.DatasetPath,
		RunnerName: r.RunnerName, RunnerVersion: r.RunnerVersion,
		StartedAt: r.StartedAt, FinishedAt: r.FinishedAt, IngestedAt: r.IngestedAt,
	}
}

func (s *Service) ListAdminJobs(ctx context.Context, limit, offset int) ([]AdminJob, int, error) {
	rows, total, err := s.Store.ListAdminJobs(ctx, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	items := make([]AdminJob, 0, len(rows))
	for _, r := range rows {
		items = append(items, adminJobFrom(r))
	}
	return items, total, nil
}

func (s *Service) GetAdminJob(ctx context.Context, jobID string) (*AdminJob, error) {
	r, err := s.Store.GetAdminJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	j := adminJobFrom(*r)
	return &j, nil
}

func nonempty(s *string) bool { return s != nil && *s != "" }

func jobListItem(r store.JobRow) JobListItem {
	return JobListItem{
		JobID:             r.JobID,
		JobName:           r.JobName,
		AgentName:         r.AgentName,
		AgentVersion:      r.AgentVersion,
		ModelName:         r.ModelName,
		ModelProvider:     r.ModelProvider,
		NTrials:           r.NTrials,
		NReward1:          r.NReward1,
		PassAt1:           r.PassAt1,
		NInputTokens:      r.NInputTokens,
		NCacheTokens:      r.NCacheTokens,
		NOutputTokens:     r.NOutputTokens,
		CostUSD:           r.CostUSD,
		NAgentSteps:       r.NAgentSteps,
		UsageReported:     r.UsageReported,
		StartedAt:         r.StartedAt,
		FinishedAt:        r.FinishedAt,
		DurationSec:       ingest.DurationSec(r.StartedAt, r.FinishedAt),
		JobType:           r.Overlay.JobType,
		EndpointClass:     r.Overlay.EndpointClass,
		AttestationStatus: r.Overlay.AttestationStatus,
		Incomparability:   r.Overlay.Incomparability,
		RunnerName:        r.Overlay.RunnerName,
		RunnerVersion:     r.Overlay.RunnerVersion,
		Listed:            r.Overlay.Listed,
	}
}

func trialListItem(t store.AnalysisTrial) TrialListItem {
	return TrialListItem{
		TrialID:       t.TrialID,
		TaskName:      t.TaskName,
		TaskChecksum:  t.TaskChecksum,
		AgentName:     t.AgentName,
		ModelName:     t.ModelName,
		Reward:        t.Reward,
		F2P:           t.F2P,
		P2P:           t.P2P,
		ExceptionType: t.ExceptionType,
		StartedAt:     t.StartedAt,
		FinishedAt:    t.FinishedAt,
		DurationSec:   ingest.DurationSec(t.StartedAt, t.FinishedAt),
		NInputTokens:  t.NInputTokens,
		NCacheTokens:  t.NCacheTokens,
		NOutputTokens: t.NOutputTokens,
		CostUSD:       t.CostUSD,
		NAgentSteps:   t.NAgentSteps,
		UsageReported: t.UsageReported,
	}
}

func majorityFromTrials(trials []store.AnalysisTrial) AgentModel {
	id := store.MajorityAgentIdentity(trials)
	return AgentModel{
		AgentName:     id.AgentName,
		AgentVersion:  id.AgentVersion,
		ModelName:     id.ModelName,
		ModelProvider: id.ModelProvider,
		Consistent:    id.Consistent,
	}
}

func addPhase(dst map[string][]float64, name string, raw *string) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return
	}
	var obj map[string]any
	if json.Unmarshal([]byte(*raw), &obj) != nil {
		return
	}
	start, _ := obj["started_at"].(string)
	finish, _ := obj["finished_at"].(string)
	if start == "" || finish == "" {
		return
	}
	d := ingest.DurationSec(&start, &finish)
	if d == nil {
		return
	}
	dst[name] = append(dst[name], *d)
}

func statsOf(xs []float64) DurationStats {
	st := DurationStats{N: len(xs)}
	if len(xs) == 0 {
		return st
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	min, max, sum := cp[0], cp[len(cp)-1], 0.0
	for _, v := range cp {
		sum += v
	}
	mean := sum / float64(len(cp))
	p50 := percentile(cp, 0.5)
	st.Min = &min
	st.Max = &max
	st.Mean = &mean
	st.P50 = &p50
	return st
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := p * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	w := pos - float64(lo)
	return sorted[lo]*(1-w) + sorted[hi]*w
}

func formatFrac(a, b int) string {
	return strings.TrimSpace(strings.Join([]string{itoa(a), "/", itoa(b)}, ""))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [32]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func orJSON(s string) string {
	if strings.TrimSpace(s) == "" {
		return "null"
	}
	return s
}

func rawOrNull(s *string) json.RawMessage {
	if s == nil || strings.TrimSpace(*s) == "" {
		return json.RawMessage("null")
	}
	if json.Valid([]byte(*s)) {
		return json.RawMessage(*s)
	}
	b, _ := json.Marshal(*s)
	return b
}
