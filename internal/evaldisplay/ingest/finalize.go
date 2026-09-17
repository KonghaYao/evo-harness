package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	objs3 "evo-harness/internal/evaldisplay/s3"
	"evo-harness/internal/evaldisplay/store"
)

var ErrNoTrials = errors.New("job has no trials")

type ValidateError struct {
	Message string
	Details []map[string]string
}

func (e *ValidateError) Error() string { return e.Message }

type Service struct {
	Store   *store.Store
	Objects objs3.ObjectStore
}

func New(st *store.Store, objects objs3.ObjectStore) *Service {
	return &Service{Store: st, Objects: objects}
}

type ParsedJob struct {
	JobName string
	Job     store.AnalysisJob
	Trials  []store.AnalysisTrial
	Overlay store.Overlay
	Files   map[string][]byte
}

func (s *Service) PrepareFinalize(ctx context.Context, jobID string) (*ParsedJob, error) {
	hub, err := s.Store.GetHubJob(ctx, jobID)
	if err != nil {
		return nil, err
	}
	files, jobName, err := s.loadJobFiles(ctx, hub)
	if err != nil {
		return nil, err
	}
	parsed, err := ParseHarborFiles(jobID, jobName, files)
	if err != nil {
		return nil, err
	}
	for key, body := range parsed.Files {
		ct := "application/octet-stream"
		switch {
		case strings.HasSuffix(key, ".json"):
			ct = "application/json"
		case strings.HasSuffix(key, ".log"), strings.HasSuffix(key, ".txt"):
			ct = "text/plain"
		}
		if err := s.Objects.Put(ctx, parsed.Job.S3Prefix+"files/"+key, bytes.NewReader(body), int64(len(body)), ct); err != nil {
			return nil, err
		}
	}
	parsed.Job.IngestedAt = store.NowRFC3339()
	return parsed, nil
}

func (s *Service) FinalizeJob(ctx context.Context, jobID string) error {
	parsed, err := s.PrepareFinalize(ctx, jobID)
	if err != nil {
		return err
	}
	return s.Store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return store.InsertFinalized(ctx, tx, parsed.Job, parsed.Trials, parsed.Overlay)
	})
}

func (s *Service) loadJobFiles(ctx context.Context, hub *store.HubJob) (map[string][]byte, string, error) {
	key := "jobs/" + hub.ID + "/job.tar.gz"
	if hub.ArchivePath != nil && strings.TrimSpace(*hub.ArchivePath) != "" {
		key = objectKeyFromArchive(*hub.ArchivePath, hub.ID)
	}
	rc, err := s.Objects.Get(ctx, key)
	if err == nil {
		defer rc.Close()
		raw, err := io.ReadAll(rc)
		if err != nil {
			return nil, "", err
		}
		files, err := UnpackTarGz(bytes.NewReader(raw))
		if err != nil {
			return nil, "", err
		}
		root, stripped := StripCommonRoot(files)
		if root != "" {
			return stripped, root, nil
		}
		jobName := ""
		if hub.JobName != nil {
			jobName = *hub.JobName
		}
		return files, jobName, nil
	}
	if !errors.Is(err, objs3.ErrNotFound) {
		return nil, "", err
	}
	trials, err := store.ListHubTrialsByJob(ctx, s.Store.DB, hub.ID, 0, 10000)
	if err != nil {
		return nil, "", err
	}
	assembled := map[string][]byte{}
	if hub.Config != nil {
		assembled["config.json"] = []byte(*hub.Config)
	}
	for _, t := range trials {
		tkey := "trials/" + t.ID + "/trial.tar.gz"
		if t.ArchivePath != nil && strings.TrimSpace(*t.ArchivePath) != "" {
			tkey = objectKeyFromArchive(*t.ArchivePath, hub.ID)
		}
		trc, err := s.Objects.Get(ctx, tkey)
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(trc)
		trc.Close()
		if err != nil {
			return nil, "", err
		}
		tf, err := UnpackTarGz(bytes.NewReader(raw))
		if err != nil {
			return nil, "", err
		}
		name := t.ID
		if t.TrialName != nil && *t.TrialName != "" {
			name = *t.TrialName
		}
		for k, v := range tf {
			assembled[path.Join(name, k)] = v
		}
	}
	jobName := ""
	if hub.JobName != nil {
		jobName = *hub.JobName
	}
	if len(assembled) == 0 {
		return nil, "", fmt.Errorf("job archive not found: %s", key)
	}
	return assembled, jobName, nil
}

func objectKeyFromArchive(archivePath, jobID string) string {
	p := strings.TrimSpace(archivePath)
	if i := strings.Index(p, "/jobs/"); i >= 0 {
		return strings.TrimPrefix(p[i+1:], "/")
	}
	if i := strings.Index(p, "/trials/"); i >= 0 {
		return strings.TrimPrefix(p[i+1:], "/")
	}
	if strings.HasPrefix(p, "jobs/") || strings.HasPrefix(p, "trials/") {
		return p
	}
	if strings.Contains(p, "job.tar.gz") {
		return "jobs/" + jobID + "/job.tar.gz"
	}
	return p
}

func ParseHarborFiles(jobID, jobName string, files map[string][]byte) (*ParsedJob, error) {
	resultRaw, ok := files["result.json"]
	if !ok {
		return nil, &ValidateError{Message: "job result.json missing"}
	}
	var jobTop map[string]any
	if err := json.Unmarshal(resultRaw, &jobTop); err != nil {
		return nil, &ValidateError{Message: "job result.json invalid"}
	}
	id, _ := jobTop["id"].(string)
	if id == "" {
		return nil, &ValidateError{Message: "job id missing"}
	}
	if !looksUUID(id) {
		return nil, &ValidateError{Message: "job id is not a UUID"}
	}
	if id != jobID {
		return nil, &ValidateError{Message: "job id mismatch", Details: []map[string]string{{"job_id": id, "expected": jobID}}}
	}
	if jobName == "" {
		if n, _ := jobTop["job_name"].(string); n != "" {
			jobName = n
		} else {
			jobName = id
		}
	}

	type trialFile struct {
		name string
		raw  []byte
	}
	var tfiles []trialFile
	for k, v := range files {
		if strings.Count(k, "/") == 1 && strings.HasSuffix(k, "/result.json") {
			tfiles = append(tfiles, trialFile{name: strings.TrimSuffix(k, "/result.json"), raw: v})
		}
	}
	if len(tfiles) == 0 {
		return nil, &ValidateError{Message: "job has no trials"}
	}
	sort.Slice(tfiles, func(i, j int) bool { return tfiles[i].name < tfiles[j].name })

	var trials []store.AnalysisTrial
	var details []map[string]string
	var nReward1 int
	var sumIn, sumCache, sumOut, sumSteps int64
	var sumCost float64
	var anyUsage bool
	var minStart, maxFinish *string

	for _, tf := range tfiles {
		tr, err := parseTrial(jobID, tf.name, tf.raw)
		if err != nil {
			var ve *ValidateError
			if errors.As(err, &ve) {
				details = append(details, ve.Details...)
				if len(ve.Details) == 0 {
					details = append(details, map[string]string{"trial_name": tf.name, "error": ve.Message})
				}
				continue
			}
			return nil, err
		}
		prefix := fmt.Sprintf("jobs/%s/files/%s/", jobID, tf.name)
		tr.S3TrialPrefix = &prefix
		trials = append(trials, *tr)
		if tr.Reward == 1 {
			nReward1++
		}
		sumIn += tr.NInputTokens
		sumCache += tr.NCacheTokens
		sumOut += tr.NOutputTokens
		sumCost += tr.CostUSD
		sumSteps += tr.NAgentSteps
		if tr.UsageReported {
			anyUsage = true
		}
		minStart = minTimePtr(minStart, tr.StartedAt)
		maxFinish = maxTimePtr(maxFinish, tr.FinishedAt)
	}
	if len(details) > 0 {
		return nil, &ValidateError{Message: "trial_invalid", Details: details}
	}
	if len(trials) == 0 {
		return nil, &ValidateError{Message: "job has no trials"}
	}

	jobUsage := UsageFromTrialObject(jobTop)
	nErr := 0
	nRetry := 0
	if v, ok := asInt(jobTop["n_errors"]); ok {
		nErr = v
	}
	if v, ok := asInt(jobTop["n_retries"]); ok {
		nRetry = v
	}
	if st, _ := jobTop["started_at"].(string); st != "" {
		minStart = &st
	}
	if ft, _ := jobTop["finished_at"].(string); ft != "" {
		maxFinish = &ft
	}

	aj := store.AnalysisJob{
		JobID:         jobID,
		JobName:       jobName,
		S3Prefix:      "jobs/" + jobID + "/",
		StartedAt:     minStart,
		FinishedAt:    maxFinish,
		NTrials:       len(trials),
		NErrors:       nErr,
		NRetries:      nRetry,
		NReward1:      nReward1,
		PassAt1:       float64(nReward1) / float64(len(trials)),
		UsageReported: jobUsage.Reported,
		IngestSHA256:  strPtr(digestResults(files)),
	}
	if jobUsage.Reported {
		aj.NInputTokens = jobUsage.NInputTokens
		aj.NCacheTokens = jobUsage.NCacheTokens
		aj.NOutputTokens = jobUsage.NOutputTokens
		aj.CostUSD = jobUsage.CostUSD
		aj.NAgentSteps = jobUsage.NAgentSteps
	} else {
		aj.NInputTokens = sumIn
		aj.NCacheTokens = sumCache
		aj.NOutputTokens = sumOut
		aj.CostUSD = sumCost
		aj.NAgentSteps = sumSteps
		aj.UsageReported = anyUsage
	}

	ov := store.Overlay{
		JobID:             jobID,
		AttestationStatus: "unsigned",
		Extra:             "{}",
	}
	ident := store.MajorityAgentIdentity(trials)
	aj.AgentName = ident.AgentName
	aj.AgentVersion = ident.AgentVersion
	aj.ModelName = ident.ModelName
	aj.ModelProvider = ident.ModelProvider
	if lockRaw, ok := files["lock.json"]; ok {
		var lock map[string]any
		if json.Unmarshal(lockRaw, &lock) == nil {
			if v, _ := lock["version"].(string); v != "" {
				ov.RunnerVersion = &v
			}
			if v, _ := lock["runner"].(string); v != "" {
				ov.RunnerName = &v
			} else if v, _ := lock["name"].(string); v != "" {
				ov.RunnerName = &v
			}
		}
	}
	if cfgRaw, ok := files["config.json"]; ok {
		var cfg map[string]any
		if json.Unmarshal(cfgRaw, &cfg) == nil {
			if env, ok := asMap(cfg["environment"]); ok {
				if t, _ := env["type"].(string); t != "" {
					ov.SandboxType = &t
				}
			}
		}
	}

	return &ParsedJob{JobName: jobName, Job: aj, Trials: trials, Overlay: ov, Files: files}, nil
}

func parseTrial(jobID, trialName string, raw []byte) (*store.AnalysisTrial, error) {
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, &ValidateError{Message: "trial result.json invalid", Details: []map[string]string{{"trial_name": trialName}}}
	}
	id, _ := top["id"].(string)
	if id == "" {
		return nil, &ValidateError{Message: "trial missing id", Details: []map[string]string{{"trial_name": trialName, "field": "id"}}}
	}
	tj := trialJobID(top)
	if tj != "" && tj != jobID {
		return nil, &ValidateError{Message: "trial job_id mismatch", Details: []map[string]string{{"trial_id": id, "field": "job_id"}}}
	}
	checksum, _ := top["task_checksum"].(string)
	if checksum == "" {
		return nil, &ValidateError{Message: "trial missing task_checksum", Details: []map[string]string{{"trial_id": id, "field": "task_checksum"}}}
	}
	taskName, _ := top["task_name"].(string)
	if taskName == "" {
		taskName = trialName
	}
	vr, _ := asMap(top["verifier_result"])
	if vr == nil {
		return nil, &ValidateError{Message: "trial missing verifier_result.rewards.reward", Details: []map[string]string{{"trial_id": id, "field": "reward"}}}
	}
	rewards, _ := asMap(vr["rewards"])
	if rewards == nil {
		return nil, &ValidateError{Message: "trial missing verifier_result.rewards.reward", Details: []map[string]string{{"trial_id": id, "field": "reward"}}}
	}
	rv, ok := numericField(rewards, "reward")
	if !ok {
		return nil, &ValidateError{Message: "trial missing verifier_result.rewards.reward", Details: []map[string]string{{"trial_id": id, "field": "reward"}}}
	}
	if rv != 0 && rv != 1 {
		return nil, &ValidateError{Message: "reward must be 0 or 1", Details: []map[string]string{{"trial_id": id, "field": "reward"}}}
	}
	ai, _ := asMap(top["agent_info"])
	agentName := ""
	if ai != nil {
		agentName, _ = ai["name"].(string)
	}
	if agentName == "" {
		return nil, &ValidateError{Message: "trial missing agent_info.name", Details: []map[string]string{{"trial_id": id, "field": "agent_info.name"}}}
	}
	agentInfoRaw, _ := json.Marshal(top["agent_info"])
	rewardsRaw, _ := json.Marshal(rewards)
	usage := UsageFromTrialObject(top)

	t := &store.AnalysisTrial{
		TrialID:         id,
		JobID:           jobID,
		TaskName:        taskName,
		TaskChecksum:    checksum,
		TrialName:       strPtr(trialName),
		AgentName:       agentName,
		AgentInfo:       string(agentInfoRaw),
		Reward:          rv,
		VerifierRewards: string(rewardsRaw),
		NInputTokens:    usage.NInputTokens,
		NCacheTokens:    usage.NCacheTokens,
		NOutputTokens:   usage.NOutputTokens,
		CostUSD:         usage.CostUSD,
		NAgentSteps:     usage.NAgentSteps,
		UsageReported:   usage.Reported,
	}
	if ai != nil {
		if v, _ := ai["version"].(string); v != "" {
			t.AgentVersion = &v
		}
		if mi, ok := asMap(ai["model_info"]); ok {
			if v, _ := mi["name"].(string); v != "" {
				t.ModelName = &v
			}
			if v, _ := mi["provider"].(string); v != "" {
				t.ModelProvider = &v
			}
		}
	}
	if t.ModelName == nil {
		if v, _ := top["model_name"].(string); v != "" {
			t.ModelName = &v
		}
	}
	if v, ok := numericField(rewards, "f2p"); ok {
		t.F2P = &v
	}
	if v, ok := numericField(rewards, "p2p"); ok {
		t.P2P = &v
	}
	if v, _ := top["task_id"].(string); v != "" {
		t.TaskID = &v
	}
	if v, _ := top["source"].(string); v != "" {
		t.Source = &v
	}
	if v, _ := top["trial_uri"].(string); v != "" {
		t.TrialURI = &v
	}
	if v, _ := top["started_at"].(string); v != "" {
		t.StartedAt = &v
	}
	if v, _ := top["finished_at"].(string); v != "" {
		t.FinishedAt = &v
	}
	if ei := top["exception_info"]; ei != nil {
		if s, ok := ei.(string); ok {
			t.ExceptionInfo = &s
		} else {
			b, _ := json.Marshal(ei)
			if string(b) != "null" {
				s := string(b)
				t.ExceptionInfo = &s
			}
		}
	}
	if v, _ := top["exception_type"].(string); v != "" {
		t.ExceptionType = &v
	}
	t.EnvironmentSetup = rawJSONPtr(top["environment_setup"])
	t.AgentSetup = rawJSONPtr(top["agent_setup"])
	t.AgentExecution = rawJSONPtr(top["agent_execution"])
	t.VerifierTiming = rawJSONPtr(top["verifier"])
	return t, nil
}

func trialJobID(top map[string]any) string {
	if s, _ := top["job_id"].(string); s != "" {
		return s
	}
	if cfg, ok := asMap(top["config"]); ok {
		if s, _ := cfg["job_id"].(string); s != "" {
			return s
		}
	}
	return ""
}

func rawJSONPtr(v any) *string {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return nil
	}
	s := string(b)
	return &s
}

func looksUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
	}
	return true
}

func digestResults(files map[string][]byte) string {
	var keys []string
	for k := range files {
		if strings.HasSuffix(k, "result.json") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte{0})
		h.Write(files[k])
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func asInt(v any) (int, bool) {
	n, ok := numericField(map[string]any{"n": v}, "n")
	if !ok {
		return 0, false
	}
	return int(n), true
}

func strPtr(s string) *string { return &s }

func minTimePtr(a, b *string) *string {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	ta, okA := ParseTime(*a)
	tb, okB := ParseTime(*b)
	if okA && okB {
		if tb.Before(ta) {
			return b
		}
		return a
	}
	if *b < *a {
		return b
	}
	return a
}

func maxTimePtr(a, b *string) *string {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	ta, okA := ParseTime(*a)
	tb, okB := ParseTime(*b)
	if okA && okB {
		if tb.After(ta) {
			return b
		}
		return a
	}
	if *b > *a {
		return b
	}
	return a
}

func ParseTime(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t.UTC(), true
		}
	}
	if t, err := time.Parse("2006-01-02T15:04:05.999999999Z", s+"Z"); err == nil {
		return t.UTC(), true
	}
	return time.Time{}, false
}

func DurationSec(start, finish *string) *float64 {
	if start == nil || finish == nil {
		return nil
	}
	a, okA := ParseTime(*start)
	b, okB := ParseTime(*finish)
	if !okA || !okB {
		return nil
	}
	d := b.Sub(a).Seconds()
	return &d
}
