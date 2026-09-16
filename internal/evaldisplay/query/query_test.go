package query

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"testing"
	"time"

	"evo-harness/internal/evaldisplay/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func insertFinalizedJob(t *testing.T, st *store.Store, idx, nTrials, nReward1 int, agent, model string, mixedOther int) string {
	t.Helper()
	ctx := context.Background()
	jobID := fmt.Sprintf("00000000-0000-4000-8000-%012d", idx)
	ingested := time.Date(2026, 9, 16, 0, 0, idx, 0, time.UTC).Format(time.RFC3339Nano)
	job := store.AnalysisJob{
		JobID:      jobID,
		JobName:    fmt.Sprintf("job-%d", idx),
		S3Prefix:   "jobs/" + jobID + "/",
		NTrials:    nTrials,
		NReward1:   nReward1,
		PassAt1:    float64(nReward1) / float64(nTrials),
		IngestedAt: ingested,
	}
	ver, prov := "agent-v3.14.2", "deepseek"
	trials := make([]store.AnalysisTrial, 0, nTrials)
	for i := 0; i < nTrials; i++ {
		reward := 0.0
		if i < nReward1 {
			reward = 1
		}
		name := fmt.Sprintf("t-%03d", i)
		an, mn := agent, model
		if mixedOther > 0 && i >= nTrials-mixedOther {
			an, mn = "other-agent", "other-model"
		}
		av, mp := ver, prov
		tid := fmt.Sprintf("11111111-1111-4111-8111-%012d", idx*1000+i)
		trials = append(trials, store.AnalysisTrial{
			TrialID:         tid,
			JobID:           jobID,
			TaskName:        "task/" + name,
			TaskChecksum:    fmt.Sprintf("sum-%d-%d", idx, i),
			TrialName:       &name,
			AgentName:       an,
			AgentVersion:    &av,
			ModelName:       &mn,
			ModelProvider:   &mp,
			AgentInfo:       `{"name":"` + an + `"}`,
			Reward:          reward,
			VerifierRewards: fmt.Sprintf(`{"reward":%g}`, reward),
		})
	}
	ov := store.Overlay{JobID: jobID, AttestationStatus: "unsigned", Extra: "{}", Listed: true}
	if err := st.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return store.InsertFinalized(ctx, tx, job, trials, ov)
	}); err != nil {
		t.Fatal(err)
	}
	return jobID
}

func TestListJobsUsesPersistedAgentAndSkipsTrialScans(t *testing.T) {
	st := openStore(t)
	ctx := context.Background()
	const nJobs, nTrials = 20, 40
	var firstID string
	for i := 0; i < nJobs; i++ {
		mixed := 0
		if i == 0 {
			mixed = 5
		}
		id := insertFinalizedJob(t, st, i+1, nTrials, 17, "peri", "deepseek-v4-flash", mixed)
		if i == 0 {
			firstID = id
		}
	}
	insertFinalizedJob(t, st, nJobs+1, 3, 1, "terminus", "other-model", 0)

	svc := New(st)
	before := st.TrialReadCount()
	items, total, err := svc.ListJobs(ctx, store.ListJobsFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	afterList := st.TrialReadCount()
	if afterList != before {
		t.Fatalf("ListJobs trial-table reads: before=%d after=%d (must not scale with jobs×trials)", before, afterList)
	}
	if total != nJobs+1 {
		t.Fatalf("total=%d", total)
	}
	if len(items) != nJobs+1 {
		t.Fatalf("len items=%d", len(items))
	}

	byID := map[string]JobListItem{}
	for _, it := range items {
		byID[it.JobID] = it
	}
	first := byID[firstID]
	if first.AgentName != "peri" || first.ModelName == nil || *first.ModelName != "deepseek-v4-flash" {
		t.Fatalf("majority agent/model on mixed job: %+v", first)
	}
	if first.NTrials != nTrials || first.NReward1 != 17 {
		t.Fatalf("counts %+v", first)
	}
	wantPass := 17.0 / float64(nTrials)
	if math.Abs(first.PassAt1-wantPass) > 1e-12 {
		t.Fatalf("pass_at_1 %v want %v", first.PassAt1, wantPass)
	}

	beforeFilter := st.TrialReadCount()
	peri := "peri"
	filtered, nPeri, err := svc.ListJobs(ctx, store.ListJobsFilter{AgentName: &peri, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if st.TrialReadCount() != beforeFilter {
		t.Fatalf("agent_name filter scanned trials: %d -> %d", beforeFilter, st.TrialReadCount())
	}
	if nPeri != nJobs {
		t.Fatalf("filter peri total=%d", nPeri)
	}
	for _, it := range filtered {
		if it.AgentName != "peri" {
			t.Fatalf("filtered item agent %q", it.AgentName)
		}
	}

	beforeStats := st.TrialReadCount()
	stt, err := svc.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.TrialReadCount() != beforeStats {
		t.Fatal("Stats must not read trials")
	}
	if stt.NJobs != nJobs+1 {
		t.Fatalf("stats n_jobs %d", stt.NJobs)
	}

	beforeGet := st.TrialReadCount()
	d, err := svc.GetJob(ctx, firstID)
	if err != nil {
		t.Fatal(err)
	}
	if st.TrialReadCount() != beforeGet {
		t.Fatalf("GetJob scanned trials: %d -> %d", beforeGet, st.TrialReadCount())
	}
	if d.AgentName != "peri" || d.PassAt1 != first.PassAt1 {
		t.Fatalf("detail %+v", d.JobListItem)
	}
}
