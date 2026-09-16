package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"evo-harness/internal/evaldisplay/conf"
	"evo-harness/internal/evaldisplay/httpapi"
	"evo-harness/internal/evaldisplay/ingest"
	"evo-harness/internal/evaldisplay/overlay"
	"evo-harness/internal/evaldisplay/query"
	objs3 "evo-harness/internal/evaldisplay/s3"
	"evo-harness/internal/evaldisplay/store"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type env struct {
	h          *server.Hertz
	token      string
	adminToken string
	anon       string
	jwt        string
}

func setup(t *testing.T) *env {
	t.Helper()
	cfg := conf.Config{
		Addr:           "127.0.0.1:0",
		SQLitePath:     filepath.Join(t.TempDir(), "eval.sqlite"),
		Token:          "test-token",
		AdminToken:     "test-admin-token",
		AnonKey:        "test-anon",
		JWTSecret:      "test-jwt-secret",
		JWTExpiresIn:   900,
		UserSub:        conf.DefaultUserSub,
		OrgID:          conf.DefaultOrgID,
		OrgName:        conf.DefaultOrgName,
		MaxUploadBytes: 32 << 20,
		S3Backend:      "memory",
	}
	st, err := store.Open(cfg.SQLitePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	objects := objs3.NewMemory()
	h := httpapi.NewServer(httpapi.Deps{
		Conf:    cfg,
		Store:   st,
		Objects: objects,
		Query:   query.New(st),
		Overlay: overlay.New(st),
		Ingest:  ingest.New(st, objects),
	})
	return &env{h: h, token: cfg.Token, adminToken: cfg.AdminToken, anon: cfg.AnonKey}
}

func (e *env) do(method, path string, body []byte, headers ...ut.Header) *ut.ResponseRecorder {
	var b *ut.Body
	if body != nil {
		b = &ut.Body{Body: bytes.NewReader(body), Len: len(body)}
	}
	return ut.PerformRequest(e.h.Engine, method, path, b, headers...)
}

func (e *env) harbor(method, path string, body []byte, extra ...ut.Header) *ut.ResponseRecorder {
	if e.jwt == "" {
		w := e.do("POST", "/functions/v1/api-key-exchange", []byte(`{"api_key":"test-token"}`),
			ut.Header{Key: "apikey", Value: e.anon},
			ut.Header{Key: "Content-Type", Value: "application/json"},
		)
		if w.Code != 200 {
			panic(fmt.Sprintf("exchange %d %s", w.Code, w.Body))
		}
		var out struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			panic(err)
		}
		if out.ExpiresIn <= 0 || out.AccessToken == "" {
			panic("expires_in must be positive")
		}
		e.jwt = out.AccessToken
	}
	hs := []ut.Header{
		{Key: "apikey", Value: e.anon},
		{Key: "Authorization", Value: "Bearer " + e.jwt},
		{Key: "Content-Type", Value: "application/json"},
	}
	hs = append(hs, extra...)
	return e.do(method, path, body, hs...)
}

func (e *env) v1(method, path string, body []byte) *ut.ResponseRecorder {
	return e.do(method, path, body,
		ut.Header{Key: "Authorization", Value: "Bearer " + e.token},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	)
}

func (e *env) admin(method, path string, body []byte) *ut.ResponseRecorder {
	return e.do(method, path, body,
		ut.Header{Key: "Authorization", Value: "Bearer " + e.adminToken},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	)
}

func TestHealthzReadyzAuth(t *testing.T) {
	e := setup(t)
	w := e.do("GET", "/healthz", nil)
	if w.Code != 200 {
		t.Fatalf("healthz %d", w.Code)
	}
	w = e.do("GET", "/readyz", nil)
	if w.Code != 200 {
		t.Fatalf("readyz %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/jobs", nil)
	if w.Code != 200 {
		t.Fatalf("public GET /v1/jobs want 200 got %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/stats", nil)
	if w.Code != 200 {
		t.Fatalf("public GET /v1/stats want 200 got %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/admin/status", nil)
	if w.Code != 401 {
		t.Fatalf("admin without token want 401 got %d %s", w.Code, w.Body)
	}
	w = e.do("PUT", "/v1/jobs/00000000-0000-4000-8000-000000000000/overlay", []byte(`{"listed":true}`))
	if w.Code != 401 {
		t.Fatalf("overlay without token want 401 got %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/admin/status", nil, ut.Header{Key: "Authorization", Value: "Bearer not-a-real-token"})
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("random bearer on admin want 401/403 got %d %s", w.Code, w.Body)
	}
}

func TestHarborCLIVerticalSlice(t *testing.T) {
	e := setup(t)

	w := e.do("POST", "/functions/v1/api-key-exchange", []byte(`{"api_key":"nope"}`),
		ut.Header{Key: "apikey", Value: e.anon},
		ut.Header{Key: "Content-Type", Value: "application/json"},
	)
	if w.Code != 401 {
		t.Fatalf("bad key %d", w.Code)
	}

	nullJobID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	usedJobID := "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	otherJobID := "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

	nullJob := ingest.SynthJob{
		ID:   nullJobID,
		Name: "synth-null-usage",
		Trials: []ingest.SynthTrial{
			{ID: "11111111-1111-4111-8111-111111111111", Name: "t-pass-a", Checksum: "c1", Reward: 1, StartedAt: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 9, 11, 10, 10, 0, 0, time.UTC)},
			{ID: "11111111-1111-4111-8111-111111111112", Name: "t-pass-b", Checksum: "c2", Reward: 1, StartedAt: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 9, 11, 10, 12, 0, 0, time.UTC)},
			{ID: "11111111-1111-4111-8111-111111111113", Name: "t-fail", Checksum: "c3", Reward: 0, StartedAt: time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC), FinishedAt: time.Date(2026, 9, 11, 10, 8, 0, 0, time.UTC)},
		},
	}
	usedJob := ingest.SynthJob{
		ID:   usedJobID,
		Name: "synth-with-usage",
		Trials: []ingest.SynthTrial{
			{ID: "22222222-2222-4222-8222-222222222221", Name: "u1", Checksum: "c1", Reward: 1, Usage: &ingest.Usage{NInputTokens: 100, NOutputTokens: 20, CostUSD: 0.5, NAgentSteps: 4}},
			{ID: "22222222-2222-4222-8222-222222222222", Name: "u2", Checksum: "c9", Reward: 0, Usage: &ingest.Usage{NInputTokens: 50, NCacheTokens: 5, NOutputTokens: 10, CostUSD: 0.25, NAgentSteps: 2}},
		},
	}
	otherJob := ingest.SynthJob{
		ID:   otherJobID,
		Name: "synth-other-dataset",
		Trials: []ingest.SynthTrial{
			{ID: "33333333-3333-4333-8333-333333333331", Name: "o1", Checksum: "c1", Reward: 1},
		},
	}

	uploadJob(t, e, nullJob)
	uploadJob(t, e, usedJob)
	uploadJob(t, e, otherJob)

	w = e.v1("GET", "/v1/jobs", nil)
	if w.Code != 200 {
		t.Fatalf("list %d %s", w.Code, w.Body)
	}
	var list struct {
		Total int              `json:"total"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 3 {
		t.Fatalf("total=%d body=%s", list.Total, w.Body)
	}
	byName := map[string]map[string]any{}
	for _, it := range list.Items {
		name, _ := it["job_name"].(string)
		byName[name] = it
		for _, k := range []string{"n_input_tokens", "n_cache_tokens", "n_output_tokens", "cost_usd", "n_agent_steps", "usage_reported"} {
			if _, ok := it[k]; !ok {
				t.Fatalf("list item missing %s", k)
			}
		}
	}
	if _, ok := byName["synth-null-usage"]; !ok {
		t.Fatal("expected separate row for synth-null-usage")
	}
	if _, ok := byName["synth-with-usage"]; !ok {
		t.Fatal("expected separate row for synth-with-usage")
	}
	nullItem := byName["synth-null-usage"]
	if nullItem["agent_name"] != "peri" {
		t.Fatalf("list agent_name %v", nullItem["agent_name"])
	}
	if nullItem["agent_version"] != "agent-v3.14.2" {
		t.Fatalf("list agent_version %v", nullItem["agent_version"])
	}
	if nullItem["model_name"] != "deepseek-v4-flash" {
		t.Fatalf("list model_name %v", nullItem["model_name"])
	}
	if nullItem["model_provider"] != "deepseek" {
		t.Fatalf("list model_provider %v", nullItem["model_provider"])
	}
	if pass, ok := nullItem["pass_at_1"].(float64); !ok || pass < 0.66 || pass > 0.67 {
		t.Fatalf("list pass_at_1 want 2/3 got %v", nullItem["pass_at_1"])
	}

	w = e.v1("GET", "/v1/jobs/"+nullJobID+"/aggregate", nil)
	if w.Code != 200 {
		t.Fatalf("agg %d %s", w.Code, w.Body)
	}
	var agg map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &agg); err != nil {
		t.Fatal(err)
	}
	if agg["pass_at_1_fraction"] != "2/3" {
		t.Fatalf("pass@1 fraction %v (69/113-style n_reward_1/n_trials)", agg["pass_at_1_fraction"])
	}
	if agg["n_trials"] != float64(3) || agg["n_reward_1"] != float64(2) {
		t.Fatalf("n %v / %v", agg["n_trials"], agg["n_reward_1"])
	}
	if agg["usage_reported"] != false {
		t.Fatalf("null usage must be usage_reported=false, not a measured zero: %v", agg["usage_reported"])
	}
	if agg["n_input_tokens"] != float64(0) || agg["cost_usd"] != float64(0) || agg["n_agent_steps"] != float64(0) {
		t.Fatalf("null usage persists as 0: %v", agg)
	}

	w = e.v1("GET", "/v1/jobs/"+usedJobID+"/aggregate", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &agg); err != nil {
		t.Fatal(err)
	}
	if agg["usage_reported"] != true {
		t.Fatalf("numeric usage must set usage_reported true: %v", agg)
	}
	if agg["n_input_tokens"] != float64(150) || agg["n_agent_steps"] != float64(6) {
		t.Fatalf("summed usage %v", agg)
	}

	w = e.v1("GET", "/v1/compare?job_a="+nullJobID+"&job_b="+otherJobID, nil)
	var cmp struct {
		DatasetMatch bool           `json:"dataset_match"`
		OverlapN     int            `json:"overlap_n"`
		JobA         map[string]any `json:"job_a"`
		JobB         map[string]any `json:"job_b"`
	}
	if w.Code != 200 {
		t.Fatalf("compare %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cmp); err != nil {
		t.Fatal(err)
	}
	if cmp.DatasetMatch {
		t.Fatal("dataset_match should be false when overlay paths differ/empty")
	}
	if cmp.OverlapN != 1 {
		t.Fatalf("overlap %d", cmp.OverlapN)
	}
	if _, ok := cmp.JobA["n_input_tokens"]; !ok {
		t.Fatal("compare must keep usage fields")
	}

	w = e.admin("PUT", "/v1/jobs/"+nullJobID+"/overlay", []byte(`{
		"incomparability":"非 Pier 官方 DeepSWE 榜。",
		"endpoint_class":"cloud",
		"runner":{"name":"pier","version":"0.3.1"},
		"fail44_equals_full_reward0": true
	}`))
	if w.Code != 200 {
		t.Fatalf("overlay %d %s", w.Code, w.Body)
	}
	w = e.v1("GET", "/v1/jobs/"+nullJobID+"?include=config,result", nil)
	if w.Code != 200 {
		t.Fatalf("detail %d %s", w.Code, w.Body)
	}
	var detail map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["endpoint_class"] != "cloud" || detail["runner_name"] != "pier" {
		t.Fatalf("overlay not applied: %v", detail)
	}
	if detail["harbor_result"] == nil {
		t.Fatal("include=result missing")
	}

	w = e.v1("GET", "/v1/jobs/"+nullJobID+"/trials", nil)
	var trials struct {
		Total int              `json:"total"`
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &trials); err != nil {
		t.Fatal(err)
	}
	if trials.Total != 3 {
		t.Fatalf("trials %d", trials.Total)
	}
	tid, _ := trials.Items[0]["trial_id"].(string)
	w = e.v1("GET", "/v1/jobs/"+nullJobID+"/trials/"+tid, nil)
	if w.Code != 200 {
		t.Fatalf("trial detail %d %s", w.Code, w.Body)
	}
	var td map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &td)
	if td["trajectory_uri"] != nil {
		t.Fatalf("v1 trajectory_uri must stay null, got %v", td["trajectory_uri"])
	}
	if _, ok := td["n_input_tokens"]; !ok {
		t.Fatal("trial usage fields required")
	}

	w = e.harbor("POST", "/rest/v1/rpc/not_a_fn", []byte(`{}`))
	if w.Code != 404 {
		t.Fatalf("rpc 404 got %d %s", w.Code, w.Body)
	}
	var rpc map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &rpc)
	if rpc["code"] != "PGRST202" {
		t.Fatalf("want PGRST202 got %v", rpc)
	}

	w = e.harbor("POST", "/rest/v1/rpc/list_my_orgs", []byte(`{}`))
	if w.Code != 200 {
		t.Fatalf("orgs %d %s", w.Code, w.Body)
	}
}

func TestTusSmallOffset(t *testing.T) {
	e := setup(t)
	payload := bytes.Repeat([]byte("abcdefgh"), 32)
	meta := "bucketName " + b64("results") + ",objectName " + b64("jobs/tus/job.log") + ",contentType " + b64("text/plain")
	w := e.harbor("POST", "/storage/v1/upload/resumable", nil,
		ut.Header{Key: "Tus-Resumable", Value: "1.0.0"},
		ut.Header{Key: "Upload-Length", Value: fmt.Sprintf("%d", len(payload))},
		ut.Header{Key: "Upload-Metadata", Value: meta},
	)
	if w.Code != 201 {
		t.Fatalf("tus create %d %s", w.Code, w.Body)
	}
	loc := string(w.Header().Get("Location"))
	if loc == "" {
		t.Fatal("missing Location")
	}
	id := loc[len(loc)-36:]
	path := "/storage/v1/upload/resumable/" + id
	w = e.harbor("HEAD", path, nil, ut.Header{Key: "Tus-Resumable", Value: "1.0.0"})
	if w.Code != 200 {
		t.Fatalf("head %d", w.Code)
	}
	mid := len(payload) / 2
	w = e.harbor("PATCH", path, payload[:mid],
		ut.Header{Key: "Tus-Resumable", Value: "1.0.0"},
		ut.Header{Key: "Content-Type", Value: "application/offset+octet-stream"},
		ut.Header{Key: "Upload-Offset", Value: "0"},
	)
	if w.Code != 204 {
		t.Fatalf("patch1 %d %s", w.Code, w.Body)
	}
	w = e.harbor("PATCH", path, payload[mid:],
		ut.Header{Key: "Tus-Resumable", Value: "1.0.0"},
		ut.Header{Key: "Content-Type", Value: "application/offset+octet-stream"},
		ut.Header{Key: "Upload-Offset", Value: fmt.Sprintf("%d", mid)},
	)
	if w.Code != 204 {
		t.Fatalf("patch2 %d %s", w.Code, w.Body)
	}
}

func TestStorageAlreadyExists(t *testing.T) {
	e := setup(t)
	body := []byte("hello")
	w := e.harbor("POST", "/storage/v1/object/results/jobs/x/job.log", body,
		ut.Header{Key: "Content-Type", Value: "text/plain"},
	)
	if w.Code != 200 {
		t.Fatalf("upload %d %s", w.Code, w.Body)
	}
	w = e.harbor("POST", "/storage/v1/object/results/jobs/x/job.log", body,
		ut.Header{Key: "Content-Type", Value: "text/plain"},
	)
	if w.Code != 409 {
		t.Fatalf("want 409 got %d %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("already exists")) {
		t.Fatalf("body must contain already exists: %s", w.Body)
	}
}

func uploadJob(t *testing.T, e *env, job ingest.SynthJob) {
	t.Helper()
	tgz, err := job.TarGz()
	if err != nil {
		t.Fatal(err)
	}
	w := e.harbor("POST", "/rest/v1/job", []byte(fmt.Sprintf(`{"id":%q,"job_name":%q,"visibility":"private"}`, job.ID, job.Name)),
		ut.Header{Key: "Prefer", Value: "return=representation"},
	)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("insert job %d %s", w.Code, w.Body)
	}
	w = e.harbor("POST", "/rest/v1/agent", []byte(`{"name":"peri","version":"agent-v3.14.2"}`))
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("agent %d %s", w.Code, w.Body)
	}
	var agents []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &agents); err != nil || len(agents) == 0 {
		t.Fatalf("agent body %s", w.Body)
	}
	agentID, _ := agents[0]["id"].(string)
	w = e.harbor("POST", "/rest/v1/model", []byte(`{"name":"deepseek-v4-flash"}`))
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("model %d %s", w.Code, w.Body)
	}
	var models []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &models)
	modelID, _ := models[0]["id"].(string)

	for _, tr := range job.Trials {
		body := fmt.Sprintf(`{"id":%q,"job_id":%q,"trial_name":%q,"task_name":%q,"task_content_hash":%q,"agent_id":%q}`,
			tr.ID, job.ID, tr.Name, tr.TaskName, tr.Checksum, agentID)
		w = e.harbor("POST", "/rest/v1/trial?on_conflict=id", []byte(body),
			ut.Header{Key: "Prefer", Value: "return=representation,resolution=ignore-duplicates"},
		)
		if w.Code != 201 && w.Code != 200 {
			t.Fatalf("trial %d %s", w.Code, w.Body)
		}
		w = e.harbor("POST", "/rest/v1/trial_model", []byte(fmt.Sprintf(`{"trial_id":%q,"model_id":%q}`, tr.ID, modelID)),
			ut.Header{Key: "Prefer", Value: "resolution=ignore-duplicates,return=minimal"},
		)
		if w.Code != 201 && w.Code != 200 && w.Code != 204 {
			t.Fatalf("trial_model %d %s", w.Code, w.Body)
		}
		w = e.harbor("PATCH", "/rest/v1/trial?id=eq."+tr.ID, []byte(fmt.Sprintf(`{"archive_path":"trials/%s/trial.tar.gz"}`, tr.ID)))
		if w.Code != 200 && w.Code != 204 {
			t.Fatalf("patch trial %d %s", w.Code, w.Body)
		}
	}
	w = e.harbor("POST", "/storage/v1/object/results/jobs/"+job.ID+"/job.tar.gz", tgz,
		ut.Header{Key: "Content-Type", Value: "application/gzip"},
	)
	if w.Code != 200 {
		t.Fatalf("storage %d %s", w.Code, w.Body)
	}
	w = e.harbor("PATCH", "/rest/v1/job?id=eq."+job.ID, []byte(fmt.Sprintf(`{"archive_path":"jobs/%s/job.tar.gz","finished_at":"2026-09-12T00:00:00Z"}`, job.ID)))
	if w.Code != 200 && w.Code != 204 {
		t.Fatalf("finalize %d %s", w.Code, w.Body)
	}
}

func TestRootAndAdminHTML(t *testing.T) {
	e := setup(t)
	w := e.do("GET", "/", nil)
	if w.Code != 200 {
		t.Fatalf("GET / %d %s", w.Code, w.Body)
	}
	body := w.Body.Bytes()
	if bytes.Contains(body, []byte(`{"service":"eval-display"}`)) {
		t.Fatalf("GET / still JSON placeholder")
	}
	if !bytes.Contains(bytes.ToLower(body), []byte("<!doctype html")) && !bytes.Contains(body, []byte("<html")) {
		t.Fatalf("GET / want HTML, got %s", body)
	}
	if bytes.Contains(body, []byte("token-form")) || bytes.Contains(body, []byte("EVAL_DISPLAY_TOKEN")) {
		t.Fatalf("GET / still asks for viewer token")
	}
	ct := w.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/html") {
		t.Fatalf("GET / Content-Type %q", ct)
	}
	w = e.do("GET", "/admin/", nil)
	if w.Code != 200 {
		t.Fatalf("GET /admin/ %d %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("后台")) {
		t.Fatalf("admin HTML missing 后台: %s", w.Body)
	}
	w = e.do("GET", "/healthz", nil)
	if w.Code != 200 {
		t.Fatalf("healthz %d", w.Code)
	}
	w = e.do("GET", "/v1/stats", nil)
	if w.Code != 200 {
		t.Fatalf("stats %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/jobs", nil)
	if w.Code != 200 {
		t.Fatalf("GET /v1/jobs want 200 got %d %s", w.Code, w.Body)
	}
}

func TestViewerForbiddenOnAdminWrites(t *testing.T) {
	e := setup(t)
	job := ingest.SynthJob{
		ID:   "dddddddd-dddd-4ddd-8ddd-dddddddddddd",
		Name: "admin-auth-job",
		Trials: []ingest.SynthTrial{
			{ID: "44444444-4444-4444-8444-444444444441", Name: "t1", Checksum: "x1", Reward: 1},
		},
	}
	uploadJob(t, e, job)

	w := e.do("GET", "/v1/jobs/"+job.ID, nil)
	if w.Code != 200 {
		t.Fatalf("public job detail %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/jobs/"+job.ID+"/trials", nil)
	if w.Code != 200 {
		t.Fatalf("public trials %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/jobs/"+job.ID+"/trials/"+job.Trials[0].ID, nil)
	if w.Code != 200 {
		t.Fatalf("public trial detail %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/jobs/"+job.ID+"/aggregate", nil)
	if w.Code != 200 {
		t.Fatalf("public aggregate %d %s", w.Code, w.Body)
	}

	w = e.do("PUT", "/v1/jobs/"+job.ID+"/overlay", []byte(`{"incomparability":"no token"}`))
	if w.Code != 401 {
		t.Fatalf("overlay without token want 401 got %d %s", w.Code, w.Body)
	}
	w = e.do("DELETE", "/v1/admin/jobs/"+job.ID, nil)
	if w.Code != 401 {
		t.Fatalf("delete without token want 401 got %d %s", w.Code, w.Body)
	}
	w = e.do("PUT", "/v1/jobs/"+job.ID+"/overlay", []byte(`{"incomparability":"random"}`),
		ut.Header{Key: "Authorization", Value: "Bearer totally-wrong"},
	)
	if w.Code != 401 && w.Code != 403 {
		t.Fatalf("random bearer overlay want 401/403 got %d %s", w.Code, w.Body)
	}

	w = e.v1("PUT", "/v1/jobs/"+job.ID+"/overlay", []byte(`{"incomparability":"viewer must not write"}`))
	if w.Code != 403 {
		t.Fatalf("viewer PUT overlay want 403 got %d %s", w.Code, w.Body)
	}
	w = e.v1("DELETE", "/v1/admin/jobs/"+job.ID, nil)
	if w.Code != 403 {
		t.Fatalf("viewer DELETE want 403 got %d %s", w.Code, w.Body)
	}
	w = e.v1("GET", "/v1/admin/status", nil)
	if w.Code != 403 {
		t.Fatalf("viewer GET admin status want 403 got %d %s", w.Code, w.Body)
	}
	w = e.admin("GET", "/v1/admin/status", nil)
	if w.Code != 200 {
		t.Fatalf("admin status %d %s", w.Code, w.Body)
	}
	var st map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"test-token", "test-admin-token", "test-jwt-secret", "AWS_SECRET", "jwt_secret"} {
		if bytes.Contains(w.Body.Bytes(), []byte(leak)) {
			t.Fatalf("admin status leaked %q: %s", leak, w.Body)
		}
	}
	if st["s3_backend"] != "memory" {
		t.Fatalf("s3_backend %v", st["s3_backend"])
	}
}

func TestAdminOverlayDeleteAndUnlisted(t *testing.T) {
	e := setup(t)
	job := ingest.SynthJob{
		ID:   "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee",
		Name: "admin-mutate-job",
		Trials: []ingest.SynthTrial{
			{ID: "55555555-5555-4555-8555-555555555551", Name: "t1", Checksum: "y1", Reward: 1},
			{ID: "55555555-5555-4555-8555-555555555552", Name: "t2", Checksum: "y2", Reward: 0},
		},
	}
	uploadJob(t, e, job)

	w := e.admin("PUT", "/v1/jobs/"+job.ID+"/overlay", []byte(`{
		"incomparability":"后台写入",
		"endpoint_class":"cloud",
		"job_type":"H",
		"attestation":{"status":"signed","attestor":"eval-attestor"},
		"listed":true
	}`))
	if w.Code != 200 {
		t.Fatalf("admin overlay %d %s", w.Code, w.Body)
	}
	w = e.do("GET", "/v1/jobs/"+job.ID, nil)
	if w.Code != 200 {
		t.Fatalf("viewer get after overlay %d %s", w.Code, w.Body)
	}
	var detail map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["attestation_status"] != "signed" || detail["listed"] != true {
		t.Fatalf("overlay not visible to viewer: %v", detail)
	}

	w = e.admin("PUT", "/v1/jobs/"+job.ID+"/overlay", []byte(`{"listed":false}`))
	if w.Code != 200 {
		t.Fatalf("unlist %d %s", w.Code, w.Body)
	}
	w = e.v1("GET", "/v1/jobs", nil)
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, it := range list.Items {
		if it["job_id"] == job.ID {
			t.Fatalf("unlisted job still in viewer list")
		}
	}
	w = e.v1("GET", "/v1/jobs/"+job.ID, nil)
	if w.Code != 404 {
		t.Fatalf("unlisted detail want 404 got %d %s", w.Code, w.Body)
	}
	w = e.admin("GET", "/v1/admin/jobs/"+job.ID, nil)
	if w.Code != 200 {
		t.Fatalf("admin still sees unlisted %d %s", w.Code, w.Body)
	}

	w = e.admin("PUT", "/v1/jobs/"+job.ID+"/overlay", []byte(`{"listed":true}`))
	if w.Code != 200 {
		t.Fatalf("relist %d %s", w.Code, w.Body)
	}
	w = e.admin("DELETE", "/v1/admin/jobs/"+job.ID, nil)
	if w.Code != 200 {
		t.Fatalf("delete %d %s", w.Code, w.Body)
	}
	w = e.v1("GET", "/v1/jobs/"+job.ID, nil)
	if w.Code != 404 {
		t.Fatalf("deleted job want 404 got %d %s", w.Code, w.Body)
	}
	w = e.admin("GET", "/v1/admin/jobs/"+job.ID, nil)
	if w.Code != 404 {
		t.Fatalf("admin deleted want 404 got %d %s", w.Code, w.Body)
	}
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
