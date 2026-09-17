package harborcompat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"evo-harness/internal/evaldisplay/ingest"
	"evo-harness/internal/evaldisplay/store"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
)

func (h *Handler) Job(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	switch string(c.Method()) {
	case "GET":
		h.getJob(ctx, c)
	case "POST":
		h.postJob(ctx, c)
	case "PATCH":
		h.patchJob(ctx, c)
	default:
		pgrstError(c, 405, "405", "method not allowed")
	}
}

func (h *Handler) getJob(ctx context.Context, c *app.RequestContext) {
	id, ok := eqParam(c, "id")
	if !ok {
		pgrstError(c, 400, "400", "id filter required")
		return
	}
	j, err := h.store.GetHubJob(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeMaybeSingle(c, nil)
		return
	}
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	writeMaybeSingle(c, []map[string]any{store.HubJobMap(j, selectCols(c))})
}

func (h *Handler) postJob(ctx context.Context, c *app.RequestContext) {
	auth, ok := h.requireHarbor(c)
	if !ok {
		return
	}
	body, err := decodeJSON(c.Request.Body())
	if err != nil {
		pgrstError(c, 400, "PGRST100", "invalid json")
		return
	}
	id := strID(body["id"])
	if id == "" {
		pgrstError(c, 400, "400", "id is required")
		return
	}
	createdBy := asText(body["created_by"])
	if createdBy == nil {
		s := h.sub(auth)
		createdBy = &s
	}
	j := store.HubJob{
		ID:             id,
		JobName:        asText(body["job_name"]),
		Config:         asText(body["config"]),
		Visibility:     asText(body["visibility"]),
		StartedAt:      asText(body["started_at"]),
		FinishedAt:     asText(body["finished_at"]),
		ArchivePath:    asText(body["archive_path"]),
		LogPath:        asText(body["log_path"]),
		NPlannedTrials: asInt64(body["n_planned_trials"]),
		OrgID:          asText(body["org_id"]),
		CreatedBy:      createdBy,
		IsHosted:       false,
	}
	err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return store.InsertHubJob(ctx, tx, j)
	})
	if store.IsUniqueErr(err) {
		c.JSON(409, map[string]any{
			"code":    "23505",
			"details": "Key (id)=(" + id + ") already exists.",
			"hint":    nil,
			"message": "duplicate key value violates unique constraint",
		})
		return
	}
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	if preferReturn(c) == "minimal" {
		c.Status(201)
		return
	}
	got, err := h.store.GetHubJob(ctx, id)
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	c.JSON(201, []map[string]any{store.HubJobMap(got, selectCols(c))})
}

func (h *Handler) patchJob(ctx context.Context, c *app.RequestContext) {
	id, ok := eqParam(c, "id")
	if !ok {
		pgrstError(c, 400, "400", "id filter required")
		return
	}
	body, err := decodeJSON(c.Request.Body())
	if err != nil {
		pgrstError(c, 400, "PGRST100", "invalid json")
		return
	}
	cur, err := h.store.GetHubJob(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		pgrstError(c, 404, "PGRST116", "job not found")
		return
	}
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}

	alreadyFinal := cur.ArchivePath != nil && strings.TrimSpace(*cur.ArchivePath) != ""
	wantArchive := false
	if v, ok := body["archive_path"]; ok && v != nil {
		if s := asText(v); s != nil && strings.TrimSpace(*s) != "" {
			wantArchive = true
		}
	}

	fields := map[string]any{}
	for _, k := range []string{"visibility", "finished_at", "log_path", "job_name", "started_at", "n_planned_trials", "org_id"} {
		if v, ok := body[k]; ok {
			fields[k] = jsonToSQL(v)
		}
	}

	if alreadyFinal {
		// Keep original archive_path; still allow visibility-only updates.
		err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			return store.PatchHubJob(ctx, tx, id, fields)
		})
		if err != nil {
			pgrstError(c, 500, "500", err.Error())
			return
		}
		h.respondJob(ctx, c, id)
		return
	}

	if wantArchive {
		parsed, ferr := h.ingest.PrepareFinalize(ctx, id)
		if ferr != nil {
			var ve *ingest.ValidateError
			if errors.As(ferr, &ve) {
				c.JSON(500, map[string]any{
					"code":    "trial_invalid",
					"message": ve.Message,
					"details": ve.Details,
				})
				return
			}
			pgrstError(c, 500, "500", ferr.Error())
			return
		}
		fields["archive_path"] = jsonToSQL(body["archive_path"])
		err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
			if err := store.InsertFinalized(ctx, tx, parsed.Job, parsed.Trials, parsed.Overlay); err != nil {
				return err
			}
			return store.PatchHubJob(ctx, tx, id, fields)
		})
		if err != nil {
			pgrstError(c, 500, "500", err.Error())
			return
		}
		h.respondJob(ctx, c, id)
		return
	}

	err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		return store.PatchHubJob(ctx, tx, id, fields)
	})
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	h.respondJob(ctx, c, id)
}

func (h *Handler) respondJob(ctx context.Context, c *app.RequestContext, id string) {
	got, err := h.store.GetHubJob(ctx, id)
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	if preferReturn(c) == "minimal" {
		c.Status(204)
		return
	}
	c.JSON(200, []map[string]any{store.HubJobMap(got, selectCols(c))})
}

func jsonToSQL(v any) any {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string, float64, bool, int, int64:
		return t
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n
		}
		if f, err := t.Float64(); err == nil {
			return f
		}
		return t.String()
	default:
		b, _ := json.Marshal(t)
		return string(b)
	}
}

func (h *Handler) Trial(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	switch string(c.Method()) {
	case "GET":
		h.getTrial(ctx, c)
	case "POST":
		h.postTrial(ctx, c)
	case "PATCH":
		h.patchTrial(ctx, c)
	default:
		pgrstError(c, 405, "405", "method not allowed")
	}
}

func (h *Handler) getTrial(ctx context.Context, c *app.RequestContext) {
	cols := selectCols(c)
	if id, ok := eqParam(c, "id"); ok {
		t, err := store.GetHubTrial(ctx, h.store.DB, id)
		if errors.Is(err, store.ErrNotFound) {
			writeMaybeSingle(c, nil)
			return
		}
		if err != nil {
			pgrstError(c, 500, "500", err.Error())
			return
		}
		writeMaybeSingle(c, []map[string]any{store.HubTrialMap(t, cols)})
		return
	}
	jobID, ok := eqParam(c, "job_id")
	if !ok {
		pgrstError(c, 400, "400", "id or job_id filter required")
		return
	}
	off, lim := parseRange(c)
	list, err := store.ListHubTrialsByJob(ctx, h.store.DB, jobID, off, lim)
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	rows := make([]map[string]any, 0, len(list))
	for i := range list {
		rows = append(rows, store.HubTrialMap(&list[i], cols))
	}
	if len(rows) > 0 {
		c.Header("Content-Range", contentRange(off, off+len(rows)-1, off+len(rows)))
	}
	if maybeSingle(c) {
		writeMaybeSingle(c, rows)
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	c.JSON(200, rows)
}

func contentRange(start, end, total int) string {
	return strings.TrimSpace(strings.Join([]string{
		itoa(start), "-", itoa(end), "/", itoa(total),
	}, ""))
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [16]byte
	i := len(b)
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

func (h *Handler) postTrial(ctx context.Context, c *app.RequestContext) {
	items, err := decodeJSONList(c.Request.Body())
	if err != nil {
		pgrstError(c, 400, "PGRST100", "invalid json")
		return
	}
	ignore := preferResolution(c) == "ignore" || strings.Contains(string(c.Query("on_conflict")), "id")
	var out []map[string]any
	err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, body := range items {
			id := strID(body["id"])
			if id == "" {
				return errors.New("trial id required")
			}
			existing, err := store.GetHubTrial(ctx, txQuery{tx}, id)
			if err == nil {
				if ignore {
					out = append(out, store.HubTrialMap(existing, selectCols(c)))
					continue
				}
				return errDup
			}
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			t := hubTrialFromMap(body)
			if err := store.InsertHubTrial(ctx, tx, t); err != nil {
				if ignore && store.IsUniqueErr(err) {
					existing, e2 := store.GetHubTrial(ctx, txQuery{tx}, id)
					if e2 != nil {
						return e2
					}
					out = append(out, store.HubTrialMap(existing, selectCols(c)))
					continue
				}
				return err
			}
			got, err := store.GetHubTrial(ctx, txQuery{tx}, id)
			if err != nil {
				return err
			}
			out = append(out, store.HubTrialMap(got, selectCols(c)))
		}
		return nil
	})
	if errors.Is(err, errDup) {
		c.JSON(409, map[string]any{"code": "23505", "message": "duplicate key value violates unique constraint"})
		return
	}
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	if preferReturn(c) == "minimal" {
		c.Status(201)
		return
	}
	c.JSON(201, out)
}

var errDup = errors.New("duplicate")

func hubTrialFromMap(body map[string]any) store.HubTrial {
	return store.HubTrial{
		ID:               strID(body["id"]),
		JobID:            strID(body["job_id"]),
		TrialName:        asText(body["trial_name"]),
		TaskName:         asText(body["task_name"]),
		TaskContentHash:  asText(body["task_content_hash"]),
		Lock:             asText(body["lock"]),
		AgentID:          asText(body["agent_id"]),
		Config:           asText(body["config"]),
		Rewards:          asText(body["rewards"]),
		ExceptionType:    asText(body["exception_type"]),
		EnvironmentSetup: asText(body["environment_setup"]),
		AgentSetup:       asText(body["agent_setup"]),
		AgentExecution:   asText(body["agent_execution"]),
		Verifier:         asText(body["verifier"]),
		ArchivePath:      asText(body["archive_path"]),
		TrajectoryPath:   asText(body["trajectory_path"]),
	}
}

func (h *Handler) patchTrial(ctx context.Context, c *app.RequestContext) {
	id, ok := eqParam(c, "id")
	if !ok {
		pgrstError(c, 400, "400", "id filter required")
		return
	}
	body, err := decodeJSON(c.Request.Body())
	if err != nil {
		pgrstError(c, 400, "PGRST100", "invalid json")
		return
	}
	fields := map[string]any{}
	for _, k := range []string{"archive_path", "trajectory_path", "rewards", "exception_type", "finished_at"} {
		if v, ok := body[k]; ok {
			fields[k] = jsonToSQL(v)
		}
	}
	var n int64
	err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var e error
		n, e = store.PatchHubTrial(ctx, tx, id, fields)
		return e
	})
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	if n != 1 {
		pgrstError(c, 404, "PGRST116", "JSON object requested, multiple (or no) rows returned")
		return
	}
	if preferReturn(c) == "minimal" {
		c.Status(204)
		return
	}
	got, err := store.GetHubTrial(ctx, h.store.DB, id)
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	c.JSON(200, []map[string]any{store.HubTrialMap(got, selectCols(c))})
}

type txQuery struct{ tx *sql.Tx }

func (t txQuery) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	return t.tx.QueryRowContext(ctx, q, args...)
}
func (t txQuery) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	return t.tx.QueryContext(ctx, q, args...)
}

func (h *Handler) Agent(ctx context.Context, c *app.RequestContext) {
	auth, ok := h.requireHarbor(c)
	if !ok {
		return
	}
	body, err := decodeJSON(c.Request.Body())
	if err != nil {
		pgrstError(c, 400, "PGRST100", "invalid json")
		return
	}
	name := strID(body["name"])
	version := strID(body["version"])
	if name == "" {
		pgrstError(c, 400, "400", "name is required")
		return
	}
	addedBy := h.sub(auth)
	if v := strID(body["added_by"]); v != "" {
		addedBy = v
	}
	var out *store.HubAgent
	err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		existing, err := store.GetHubAgent(ctx, txQuery{tx}, addedBy, name, version)
		if err == nil {
			out = existing
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		a := store.HubAgent{ID: uuid.NewString(), AddedBy: addedBy, Name: name, Version: version}
		if id := strID(body["id"]); id != "" {
			a.ID = id
		}
		if err := store.InsertHubAgent(ctx, tx, a); err != nil {
			if store.IsUniqueErr(err) {
				existing, e2 := store.GetHubAgent(ctx, txQuery{tx}, addedBy, name, version)
				if e2 != nil {
					return e2
				}
				out = existing
				return nil
			}
			return err
		}
		out = &a
		return nil
	})
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	c.JSON(201, []map[string]any{{
		"id":       out.ID,
		"name":     out.Name,
		"version":  out.Version,
		"added_by": out.AddedBy,
	}})
}

func (h *Handler) Model(ctx context.Context, c *app.RequestContext) {
	auth, ok := h.requireHarbor(c)
	if !ok {
		return
	}
	body, err := decodeJSON(c.Request.Body())
	if err != nil {
		pgrstError(c, 400, "PGRST100", "invalid json")
		return
	}
	name := strID(body["name"])
	if name == "" {
		pgrstError(c, 400, "400", "name is required")
		return
	}
	provider := "unknown"
	if v, ok := body["provider"]; ok && v != nil {
		if s := strID(v); s != "" {
			provider = s
		}
	}
	addedBy := h.sub(auth)
	var out *store.HubModel
	err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		existing, err := store.GetHubModel(ctx, txQuery{tx}, addedBy, name, provider)
		if err == nil {
			out = existing
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		m := store.HubModel{ID: uuid.NewString(), AddedBy: addedBy, Name: name, Provider: provider}
		if id := strID(body["id"]); id != "" {
			m.ID = id
		}
		if err := store.InsertHubModel(ctx, tx, m); err != nil {
			if store.IsUniqueErr(err) {
				existing, e2 := store.GetHubModel(ctx, txQuery{tx}, addedBy, name, provider)
				if e2 != nil {
					return e2
				}
				out = existing
				return nil
			}
			return err
		}
		out = &m
		return nil
	})
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	c.JSON(201, []map[string]any{{
		"id":       out.ID,
		"name":     out.Name,
		"provider": out.Provider,
		"added_by": out.AddedBy,
	}})
}

func (h *Handler) TrialModel(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	items, err := decodeJSONList(c.Request.Body())
	if err != nil {
		pgrstError(c, 400, "PGRST100", "invalid json")
		return
	}
	err = h.store.Write(ctx, func(ctx context.Context, tx *sql.Tx) error {
		for _, body := range items {
			trialID := strID(body["trial_id"])
			modelID := strID(body["model_id"])
			if trialID == "" || modelID == "" {
				return errors.New("trial_id and model_id required")
			}
			if err := store.InsertHubTrialModelIgnore(ctx, tx, trialID, modelID, asInt64(body["n_input_tokens"]), asInt64(body["n_cache_tokens"]), asInt64(body["n_output_tokens"]), asFloat(body["cost_usd"])); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		pgrstError(c, 500, "500", err.Error())
		return
	}
	if preferReturn(c) == "minimal" {
		c.Status(201)
		return
	}
	c.JSON(201, items)
}

func (h *Handler) ListMyOrgs(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	c.JSON(200, []map[string]any{{
		"id":           h.cfg.OrgID,
		"name":         h.cfg.OrgName,
		"display_name": h.cfg.OrgName,
		"kind":         "personal",
		"role":         "owner",
	}})
}

func (h *Handler) RPCNotFound(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	name := c.Param("name")
	c.JSON(404, map[string]any{
		"code":    "PGRST202",
		"details": nil,
		"hint":    nil,
		"message": "Could not find the function public." + name + " without parameters in the schema cache",
	})
}
