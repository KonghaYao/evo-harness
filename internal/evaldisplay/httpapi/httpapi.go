package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"evo-harness/internal/evaldisplay/conf"
	"evo-harness/internal/evaldisplay/harborcompat"
	"evo-harness/internal/evaldisplay/ingest"
	"evo-harness/internal/evaldisplay/overlay"
	"evo-harness/internal/evaldisplay/query"
	objs3 "evo-harness/internal/evaldisplay/s3"
	"evo-harness/internal/evaldisplay/static"
	"evo-harness/internal/evaldisplay/store"

	"crypto/subtle"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type Deps struct {
	Conf    conf.Config
	Store   *store.Store
	Objects objs3.ObjectStore
	Query   *query.Service
	Overlay *overlay.Service
	Ingest  *ingest.Service
}

func NewServer(d Deps) *server.Hertz {
	h := server.New(
		server.WithHostPorts(d.Conf.Addr),
		server.WithMaxRequestBodySize(int(d.Conf.MaxUploadBytes)),
		server.WithExitWaitTime(0),
	)
	Register(h, d)
	return h
}

func Register(h *server.Hertz, d Deps) {
	adminTok := d.Conf.AdminBearer()
	h.Use(func(ctx context.Context, c *app.RequestContext) {
		path := string(c.Path())
		method := string(c.Method())
		if !isAdminAPI(method, path) {
			c.Next(ctx)
			return
		}
		auth := string(c.GetHeader("Authorization"))
		if bearerOK(auth, adminTok) {
			c.Next(ctx)
			return
		}
		if d.Conf.Token != "" && bearerOK(auth, d.Conf.Token) {
			writeErr(c, 403, "forbidden", "admin token required", nil)
			c.Abort()
			return
		}
		writeErr(c, 401, "unauthorized", "missing or invalid bearer token", nil)
		c.Abort()
	})

	h.GET("/healthz", func(ctx context.Context, c *app.RequestContext) {
		c.JSON(200, map[string]any{"status": "ok"})
	})
	h.GET("/readyz", func(ctx context.Context, c *app.RequestContext) {
		if err := d.Store.Ping(ctx); err != nil {
			writeErr(c, 503, "db_unavailable", err.Error(), nil)
			return
		}
		c.JSON(200, map[string]any{"status": "ready"})
	})

	ing := d.Ingest
	if ing == nil {
		ing = ingest.New(d.Store, d.Objects)
	}
	hh := harborcompat.New(d.Conf, d.Store, d.Objects, ing)
	harborcompat.Register(h, hh)

	h.GET("/v1/stats", func(ctx context.Context, c *app.RequestContext) {
		st, err := d.Query.Stats(ctx)
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		c.JSON(200, st)
	})

	h.GET("/v1/admin/status", func(ctx context.Context, c *app.RequestContext) {
		ready := d.Store.Ping(ctx) == nil
		status := "ready"
		if !ready {
			status = "degraded"
		}
		nHub, _ := d.Store.CountHubJobs(ctx)
		nFin, _ := d.Store.CountFinalizedJobs(ctx)
		c.JSON(200, map[string]any{
			"status":           status,
			"ready":            ready,
			"sqlite_path":      d.Conf.SQLitePath,
			"s3_backend":       d.Conf.S3Backend,
			"n_hub_jobs":       nHub,
			"n_finalized_jobs": nFin,
		})
	})

	h.GET("/v1/admin/jobs/:job_id", func(ctx context.Context, c *app.RequestContext) {
		jobID := c.Param("job_id")
		j, err := d.Query.GetAdminJob(ctx, jobID)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "job_not_found", "job not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		c.JSON(200, j)
	})

	h.DELETE("/v1/admin/jobs/:job_id", func(ctx context.Context, c *app.RequestContext) {
		jobID := c.Param("job_id")
		res, err := d.Store.DeleteJob(ctx, jobID)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "job_not_found", "job not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		if d.Objects != nil && res != nil {
			for _, p := range res.Prefixes {
				_ = d.Objects.DeletePrefix(ctx, p)
			}
		}
		c.JSON(200, map[string]any{"job_id": jobID, "deleted": true})
	})

	h.GET("/v1/admin/jobs", func(ctx context.Context, c *app.RequestContext) {
		limit, offset := 50, 0
		if v := string(c.Query("limit")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				limit = n
			}
		}
		if v := string(c.Query("offset")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				offset = n
			}
		}
		items, total, err := d.Query.ListAdminJobs(ctx, limit, offset)
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		if items == nil {
			items = []query.AdminJob{}
		}
		c.JSON(200, map[string]any{"total": total, "items": items})
	})

	h.GET("/v1/compare", func(ctx context.Context, c *app.RequestContext) {
		a := string(c.Query("job_a"))
		b := string(c.Query("job_b"))
		if a == "" || b == "" {
			writeErr(c, 400, "bad_request", "job_a and job_b are required", nil)
			return
		}
		out, err := d.Query.Compare(ctx, a, b)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "job_not_found", "job not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		c.JSON(200, out)
	})

	h.GET("/v1/jobs/:job_id/trials/:trial_id", func(ctx context.Context, c *app.RequestContext) {
		jobID := c.Param("job_id")
		trialID := c.Param("trial_id")
		t, err := d.Query.GetTrial(ctx, jobID, trialID)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "trial_not_found", "trial not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		j, err := d.Store.GetAnalysisJob(ctx, jobID)
		if err == nil && t != nil {
			name := ""
			if tr, e := d.Store.GetTrial(ctx, jobID, trialID); e == nil && tr.TrialName != nil {
				name = *tr.TrialName
			}
			cfg, res := loadTrialJSON(ctx, d.Objects, j.S3Prefix, name, trialID)
			t.HarborConfig = cfg
			t.HarborResult = res
		}
		c.JSON(200, t)
	})

	h.GET("/v1/jobs/:job_id/trials", func(ctx context.Context, c *app.RequestContext) {
		jobID := c.Param("job_id")
		f := store.ListTrialsFilter{JobID: jobID, Order: string(c.Query("order"))}
		if v := string(c.Query("limit")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				f.Limit = n
			}
		}
		if v := string(c.Query("offset")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				f.Offset = n
			}
		}
		if v := string(c.Query("reward")); v != "" {
			if n, err := strconv.ParseFloat(v, 64); err == nil {
				f.Reward = &n
			}
		}
		if v := string(c.Query("task_name")); v != "" {
			f.TaskName = &v
		}
		items, total, err := d.Query.ListTrials(ctx, f)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "job_not_found", "job not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		c.JSON(200, map[string]any{"job_id": jobID, "total": total, "items": items})
	})

	h.GET("/v1/jobs/:job_id/aggregate", func(ctx context.Context, c *app.RequestContext) {
		jobID := c.Param("job_id")
		agg, err := d.Query.Aggregate(ctx, jobID, true)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "job_not_found", "job not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		c.JSON(200, agg)
	})

	h.PUT("/v1/jobs/:job_id/overlay", func(ctx context.Context, c *app.RequestContext) {
		jobID := c.Param("job_id")
		var body map[string]any
		if err := json.Unmarshal(c.Request.Body(), &body); err != nil {
			writeErr(c, 400, "bad_request", "invalid json", nil)
			return
		}
		ov, err := d.Overlay.Put(ctx, jobID, body)
		var pe *overlay.PutError
		if errors.As(err, &pe) {
			status := 422
			if pe.Code == "overlay_readonly" {
				status = 422
			}
			writeErr(c, status, pe.Code, pe.Message, nil)
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "job_not_found", "job not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		c.JSON(200, overlay.OverlayResponse(ov))
	})

	h.GET("/v1/jobs/:job_id", func(ctx context.Context, c *app.RequestContext) {
		jobID := c.Param("job_id")
		djob, err := d.Query.GetJob(ctx, jobID)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(c, 404, "job_not_found", "job not found", nil)
			return
		}
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		inc := string(c.Query("include"))
		if strings.Contains(inc, "config") || strings.Contains(inc, "result") {
			j, err := d.Store.GetAnalysisJob(ctx, jobID)
			if err == nil {
				if strings.Contains(inc, "config") {
					djob.HarborConfig = getJSON(ctx, d.Objects, j.S3Prefix+"files/config.json")
				}
				if strings.Contains(inc, "result") {
					djob.HarborResult = getJSON(ctx, d.Objects, j.S3Prefix+"files/result.json")
				}
			}
		}
		c.JSON(200, djob)
	})

	h.GET("/v1/jobs", func(ctx context.Context, c *app.RequestContext) {
		f := store.ListJobsFilter{}
		if v := string(c.Query("limit")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				f.Limit = n
			}
		}
		if v := string(c.Query("offset")); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				f.Offset = n
			}
		}
		if q := c.Request.URI().QueryArgs(); q != nil {
			if q.Has("job_name") {
				s := string(c.Query("job_name"))
				f.JobName = &s
			}
			if q.Has("agent_name") {
				s := string(c.Query("agent_name"))
				f.AgentName = &s
			}
			if q.Has("model_name") {
				s := string(c.Query("model_name"))
				f.ModelName = &s
			}
			if q.Has("job_type") {
				s := string(c.Query("job_type"))
				f.JobType = &s
			}
			if q.Has("attestation_status") {
				s := string(c.Query("attestation_status"))
				f.AttestationStatus = &s
			}
		}
		items, total, err := d.Query.ListJobs(ctx, f)
		if err != nil {
			writeErr(c, 500, "internal", err.Error(), nil)
			return
		}
		if items == nil {
			items = []query.JobListItem{}
		}
		c.JSON(200, map[string]any{"total": total, "items": items})
	})

	static.Register(h, d.Conf.StaticDir)
}

func isAdminAPI(method, path string) bool {
	if strings.HasPrefix(path, "/v1/admin/") || path == "/v1/admin" {
		return true
	}
	if strings.EqualFold(method, "PUT") && strings.HasPrefix(path, "/v1/jobs/") && strings.HasSuffix(path, "/overlay") {
		return true
	}
	return false
}

func bearerOK(header, token string) bool {
	if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return false
	}
	got := strings.TrimSpace(header[7:])
	if token == "" || got == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(token)) == 1
}

func writeErr(c *app.RequestContext, status int, code, message string, details any) {
	if details == nil {
		c.JSON(status, map[string]any{"error": map[string]any{"code": code, "message": message}})
		return
	}
	c.JSON(status, map[string]any{"error": map[string]any{"code": code, "message": message, "details": details}})
}

func getJSON(ctx context.Context, objects objs3.ObjectStore, key string) json.RawMessage {
	rc, err := objects.Get(ctx, key)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil || len(b) == 0 {
		return nil
	}
	if !json.Valid(b) {
		enc, _ := json.Marshal(string(b))
		return enc
	}
	return json.RawMessage(b)
}

func loadTrialJSON(ctx context.Context, objects objs3.ObjectStore, s3Prefix, trialName, trialID string) (cfg, res json.RawMessage) {
	candidates := []string{}
	if trialName != "" {
		candidates = append(candidates, s3Prefix+"files/"+trialName+"/")
	}
	candidates = append(candidates, s3Prefix+"files/"+trialID+"/")
	for _, p := range candidates {
		if cfg == nil {
			cfg = getJSON(ctx, objects, p+"config.json")
		}
		if res == nil {
			res = getJSON(ctx, objects, p+"result.json")
		}
	}
	return cfg, res
}
