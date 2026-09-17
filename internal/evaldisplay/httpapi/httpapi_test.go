package httpapi_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

func (e *env) ensureJWT() {
	if e.jwt != "" {
		return
	}
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

func (e *env) harbor(method, path string, body []byte, extra ...ut.Header) *ut.ResponseRecorder {
	e.ensureJWT()
	hasCT := false
	for _, h := range extra {
		if strings.EqualFold(h.Key, "Content-Type") || strings.EqualFold(h.Key, "content-type") {
			hasCT = true
			break
		}
	}
	hs := []ut.Header{
		{Key: "apikey", Value: e.anon},
		{Key: "Authorization", Value: "Bearer " + e.jwt},
	}
	if !hasCT {
		hs = append(hs, ut.Header{Key: "Content-Type", Value: "application/json"})
	}
	hs = append(hs, extra...)
	return e.do(method, path, body, hs...)
}

// tus is Harbor resumable.py: Bearer JWT only, no apikey header.
func (e *env) tus(method, path string, body []byte, extra ...ut.Header) *ut.ResponseRecorder {
	e.ensureJWT()
	hs := []ut.Header{
		{Key: "Authorization", Value: "Bearer " + e.jwt},
		{Key: "Tus-Resumable", Value: "1.0.0"},
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
		for _, k := range []string{"n_input_tokens", "n_cache_tokens", "n_output_tokens", "cost_usd", "n_agent_steps", "usage_reported", "dataset_name", "job_name"} {
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
	w := e.tus("POST", "/storage/v1/upload/resumable", nil,
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
	if strings.Contains(loc, "://") {
		t.Fatalf("TUS Location should be relative so CLI origin check passes, got %q", loc)
	}
	id := loc[len(loc)-36:]
	path := "/storage/v1/upload/resumable/" + id
	w = e.tus("HEAD", path, nil)
	if w.Code != 200 {
		t.Fatalf("head %d", w.Code)
	}
	mid := len(payload) / 2
	w = e.tus("PATCH", path, payload[:mid],
		ut.Header{Key: "Content-Type", Value: "application/offset+octet-stream"},
		ut.Header{Key: "Upload-Offset", Value: "0"},
	)
	if w.Code != 204 {
		t.Fatalf("patch1 %d %s", w.Code, w.Body)
	}
	w = e.tus("PATCH", path, payload[mid:],
		ut.Header{Key: "Content-Type", Value: "application/offset+octet-stream"},
		ut.Header{Key: "Upload-Offset", Value: fmt.Sprintf("%d", mid)},
	)
	if w.Code != 204 {
		t.Fatalf("patch2 %d %s", w.Code, w.Body)
	}
}

func TestHarborMaybeSingleAndCORS(t *testing.T) {
	e := setup(t)
	missing := "ffffffff-ffff-4fff-8fff-ffffffffffff"
	w := e.harbor("GET", "/rest/v1/job?select=visibility&id=eq."+missing, nil,
		ut.Header{Key: "Accept", Value: "application/vnd.pgrst.object+json"},
	)
	if w.Code != 406 {
		t.Fatalf("maybe_single empty want 406 got %d %s", w.Code, w.Body)
	}
	var errBody map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &errBody); err != nil {
		t.Fatal(err)
	}
	details, _ := errBody["details"].(string)
	if !strings.Contains(details, "The result contains 0 rows") {
		t.Fatalf("postgrest-py maybe_single needs details containing 0 rows, got %v", errBody)
	}

	w = e.do("OPTIONS", "/rest/v1/job", nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "GET"},
		ut.Header{Key: "Access-Control-Request-Headers", Value: "apikey,authorization"},
	)
	if w.Code != 204 {
		t.Fatalf("OPTIONS /rest/v1/job want 204 got %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Fatalf("missing CORS Allow-Origin: %v", w.Header())
	}
	w = e.do("OPTIONS", "/storage/v1/upload/resumable", nil,
		ut.Header{Key: "Origin", Value: "http://127.0.0.1:3000"},
		ut.Header{Key: "Access-Control-Request-Method", Value: "POST"},
	)
	if w.Code != 204 {
		t.Fatalf("OPTIONS TUS want 204 got %d %s", w.Code, w.Body)
	}
	if w.Header().Get("Tus-Resumable") != "1.0.0" {
		t.Fatalf("OPTIONS TUS missing Tus-Resumable: %v", w.Header())
	}
}

func TestHarborCLISequence(t *testing.T) {
	e := setup(t)
	jobID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	trialID := "11111111-1111-4111-8111-111111111111"
	objectJSON := ut.Header{Key: "Accept", Value: "application/vnd.pgrst.object+json"}
	preferRep := ut.Header{Key: "Prefer", Value: "return=representation"}
	preferUpsert := ut.Header{Key: "Prefer", Value: "return=representation,resolution=merge-duplicates"}
	preferIgnore := ut.Header{Key: "Prefer", Value: "return=representation,resolution=ignore-duplicates"}

	w := e.harbor("GET", "/rest/v1/job?select=visibility&id=eq."+jobID, nil, objectJSON)
	if w.Code != 406 {
		t.Fatalf("start_job visibility probe want 406 got %d %s", w.Code, w.Body)
	}

	w = e.harbor("POST", "/rest/v1/rpc/list_my_orgs", []byte(`{}`))
	if w.Code != 200 {
		t.Fatalf("list_my_orgs %d %s", w.Code, w.Body)
	}
	var orgs []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &orgs); err != nil || len(orgs) == 0 {
		t.Fatalf("orgs %s", w.Body)
	}
	if orgs[0]["kind"] != "personal" {
		t.Fatalf("want personal org, got %v", orgs[0])
	}
	orgID, _ := orgs[0]["id"].(string)

	jobBody := fmt.Sprintf(`{"id":%q,"job_name":"cli-seq","started_at":"2026-09-11T10:00:00Z","config":{"job_name":"cli-seq"},"visibility":"private","org_id":%q,"n_planned_trials":2}`, jobID, orgID)
	w = e.harbor("POST", "/rest/v1/job", []byte(jobBody), preferRep)
	if w.Code != 201 {
		t.Fatalf("insert job %d %s", w.Code, w.Body)
	}
	w = e.harbor("GET", "/rest/v1/job?select=org_id,organization(id,name,display_name,kind)&id=eq."+jobID, nil, objectJSON)
	if w.Code != 200 {
		t.Fatalf("get_job_owner_org %d %s", w.Code, w.Body)
	}
	var owned map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &owned); err != nil {
		t.Fatal(err)
	}
	if owned["org_id"] != orgID {
		t.Fatalf("org_id %v", owned["org_id"])
	}
	orgEmbed, _ := owned["organization"].(map[string]any)
	if orgEmbed == nil || orgEmbed["id"] != orgID {
		t.Fatalf("organization embed %v", owned["organization"])
	}

	w = e.harbor("POST", "/rest/v1/agent?on_conflict=added_by,name,version", []byte(`{"name":"peri","version":"agent-v3.14.2"}`), preferUpsert)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("agent %d %s", w.Code, w.Body)
	}
	var agents []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &agents); err != nil || len(agents) == 0 || agents[0]["id"] == nil {
		t.Fatalf("agent must return data[0].id: %s", w.Body)
	}
	agentID, _ := agents[0]["id"].(string)

	w = e.harbor("POST", "/rest/v1/model?on_conflict=added_by,name,provider", []byte(`{"name":"deepseek-v4-flash"}`), preferUpsert)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("model %d %s", w.Code, w.Body)
	}
	var models []map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &models)
	if len(models) == 0 || models[0]["id"] == nil {
		t.Fatalf("model must return data[0].id: %s", w.Body)
	}
	modelID, _ := models[0]["id"].(string)

	w = e.harbor("GET", "/rest/v1/trial?select=id,trial_name,archive_path&id=eq."+trialID, nil, objectJSON)
	if w.Code != 406 {
		t.Fatalf("get_trial empty want 406 got %d %s", w.Code, w.Body)
	}

	trialBody := fmt.Sprintf(`{"id":%q,"trial_name":"t-pass-a","task_name":"t-pass-a","task_content_hash":"c1","lock":{"schema_version":2},"job_id":%q,"agent_id":%q,"config":{},"rewards":{"reward":1}}`, trialID, jobID, agentID)
	w = e.harbor("POST", "/rest/v1/trial?on_conflict=id", []byte(trialBody), preferIgnore)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("insert trial %d %s", w.Code, w.Body)
	}
	w = e.harbor("POST", "/rest/v1/trial_model?on_conflict=trial_id,model_id", []byte(fmt.Sprintf(`{"trial_id":%q,"model_id":%q}`, trialID, modelID)),
		ut.Header{Key: "Prefer", Value: "return=minimal,resolution=ignore-duplicates"})
	if w.Code != 201 && w.Code != 200 && w.Code != 204 {
		t.Fatalf("trial_model %d %s", w.Code, w.Body)
	}

	job := ingest.SynthJob{
		ID:   jobID,
		Name: "cli-seq",
		Trials: []ingest.SynthTrial{
			{ID: trialID, Name: "t-pass-a", Checksum: "c1", Reward: 1},
			{ID: "11111111-1111-4111-8111-111111111112", Name: "t-fail", Checksum: "c2", Reward: 0},
		},
	}
	trial2 := job.Trials[1]
	trial2Body := fmt.Sprintf(`{"id":%q,"trial_name":%q,"task_name":%q,"task_content_hash":%q,"lock":{"schema_version":2},"job_id":%q,"agent_id":%q}`,
		trial2.ID, trial2.Name, trial2.Name, trial2.Checksum, jobID, agentID)
	w = e.harbor("POST", "/rest/v1/trial?on_conflict=id", []byte(trial2Body), preferIgnore)
	if w.Code != 201 && w.Code != 200 {
		t.Fatalf("trial2 %d %s", w.Code, w.Body)
	}

	tgz, err := job.TarGz()
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range job.Trials {
		trialTar, err := ingest.PackTarGz(map[string][]byte{"result.json": []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		meta := "bucketName " + b64("results") + ",objectName " + b64("trials/"+tr.ID+"/trial.tar.gz") + ",contentType " + b64("application/gzip")
		w = e.tus("POST", "/storage/v1/upload/resumable", nil,
			ut.Header{Key: "Upload-Length", Value: fmt.Sprintf("%d", len(trialTar))},
			ut.Header{Key: "Upload-Metadata", Value: meta},
		)
		if w.Code != 201 {
			t.Fatalf("tus trial create %d %s", w.Code, w.Body)
		}
		loc := string(w.Header().Get("Location"))
		path := loc
		if !strings.HasPrefix(path, "/") {
			t.Fatalf("relative Location required, got %q", loc)
		}
		w = e.tus("PATCH", path, trialTar,
			ut.Header{Key: "Content-Type", Value: "application/offset+octet-stream"},
			ut.Header{Key: "Upload-Offset", Value: "0"},
		)
		if w.Code != 204 {
			t.Fatalf("tus trial patch %d %s", w.Code, w.Body)
		}
		w = e.harbor("PATCH", "/rest/v1/trial?id=eq."+tr.ID, []byte(fmt.Sprintf(`{"archive_path":"trials/%s/trial.tar.gz","trajectory_path":null}`, tr.ID)), preferRep)
		if w.Code != 200 {
			t.Fatalf("finalize trial %d %s", w.Code, w.Body)
		}
		var patched []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &patched); err != nil || len(patched) != 1 || patched[0]["id"] != tr.ID {
			t.Fatalf("finalize_trial_artifacts requires representation id, got %s", w.Body)
		}
	}

	meta := "bucketName " + b64("results") + ",objectName " + b64("jobs/"+jobID+"/job.tar.gz") + ",contentType " + b64("application/gzip")
	w = e.tus("POST", "/storage/v1/upload/resumable", nil,
		ut.Header{Key: "Upload-Length", Value: fmt.Sprintf("%d", len(tgz))},
		ut.Header{Key: "Upload-Metadata", Value: meta},
	)
	if w.Code != 201 {
		t.Fatalf("tus job create %d %s", w.Code, w.Body)
	}
	w = e.tus("PATCH", string(w.Header().Get("Location")), tgz,
		ut.Header{Key: "Content-Type", Value: "application/offset+octet-stream"},
		ut.Header{Key: "Upload-Offset", Value: "0"},
	)
	if w.Code != 204 {
		t.Fatalf("tus job patch %d %s", w.Code, w.Body)
	}

	w = e.harbor("GET", "/rest/v1/job?select=id,job_name,archive_path,config,started_at,finished_at,n_planned_trials&id=eq."+jobID, nil, objectJSON)
	if w.Code != 200 {
		t.Fatalf("get_job %d %s", w.Code, w.Body)
	}
	var remote map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &remote); err != nil {
		t.Fatal(err)
	}
	if remote["archive_path"] != nil {
		t.Fatalf("archive_path should still be null before finalize, got %v", remote["archive_path"])
	}

	w = e.harbor("PATCH", "/rest/v1/job?id=eq."+jobID, []byte(fmt.Sprintf(`{"archive_path":"jobs/%s/job.tar.gz","finished_at":"2026-09-12T00:00:00Z"}`, jobID)), preferRep)
	if w.Code != 200 {
		t.Fatalf("finalize job %d %s", w.Code, w.Body)
	}

	w = e.do("GET", "/v1/jobs/"+jobID, nil)
	if w.Code != 200 {
		t.Fatalf("viewer after harbor upload %d %s", w.Code, w.Body)
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

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "job.log")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("log-from-multipart")); err != nil {
		t.Fatal(err)
	}
	ct := mw.FormDataContentType()
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	w = e.harbor("POST", "/storage/v1/object/results/jobs/x/from-form.log", buf.Bytes(),
		ut.Header{Key: "Content-Type", Value: ct},
	)
	if w.Code != 200 {
		t.Fatalf("multipart upload %d %s", w.Code, w.Body)
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
	w = e.do("GET", "/app.js", nil)
	if w.Code != 200 {
		t.Fatalf("GET /app.js %d", w.Code)
	}
	js := w.Body.Bytes()
	if !bytes.Contains(js, []byte("完成度")) || !bytes.Contains(js, []byte("n_agent_steps")) {
		t.Fatalf("viewer chart missing 完成度 / n_agent_steps")
	}
	if !bytes.Contains(js, []byte("n_input_tokens + n_cache_tokens + n_output_tokens")) {
		t.Fatalf("token X axis must sum the three reported token fields")
	}
	if !bytes.Contains(js, []byte("未报告用量")) {
		t.Fatalf("chart must label unreported usage")
	}
	if !bytes.Contains(js, []byte("chart-seg-btn")) {
		t.Fatalf("X axis must be segmented buttons")
	}
	if !bytes.Contains(js, []byte("reverse: true")) && !bytes.Contains(js, []byte("reverse:true")) {
		t.Fatalf("X scale must reverse so fewer tokens/cost/steps sit on the right")
	}
	if !bytes.Contains(js, []byte("向右更少")) || !bytes.Contains(js, []byte("harness")) {
		t.Fatalf("chart copy missing 向右更少 / harness identity")
	}
	if !bytes.Contains(js, []byte("data-board")) {
		t.Fatalf("viewer must expose leaderboard tabs")
	}
	if !bytes.Contains(js, []byte("peri-3142-full")) || !bytes.Contains(js, []byte("peri-3142-fail44")) {
		t.Fatalf("full and fail44 must be separate board keys")
	}
	if !bytes.Contains(js, []byte("不合并 Pass@1")) {
		t.Fatalf("tabs must say full and fail44 are not merged")
	}
	if !bytes.Contains(js, []byte("榜单名称")) {
		t.Fatalf("hover/detail must show 榜单名称")
	}
	w = e.do("GET", "/chart.umd.min.js", nil)
	if w.Code != 200 {
		t.Fatalf("GET /chart.umd.min.js %d", w.Code)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("Chart.js")) {
		t.Fatalf("chart bundle missing Chart.js header")
	}
	w = e.do("GET", "/admin/", nil)
	if w.Code != 200 || !bytes.Contains(w.Body.Bytes(), []byte("后台")) {
		t.Fatalf("admin page broken after chart: %d %s", w.Code, w.Body)
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
