package harborcompat

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"evo-harness/internal/evaldisplay/conf"
	"evo-harness/internal/evaldisplay/ingest"
	objs3 "evo-harness/internal/evaldisplay/s3"
	"evo-harness/internal/evaldisplay/store"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/golang-jwt/jwt/v5"
)

type Handler struct {
	cfg     conf.Config
	store   *store.Store
	objects objs3.ObjectStore
	ingest  *ingest.Service

	tusMu sync.Mutex
	tus   map[string]*tusUpload
}

type tusUpload struct {
	ID          string
	ObjectName  string
	Bucket      string
	Length      int64
	Offset      int64
	ContentType string
	Buf         []byte
}

func New(cfg conf.Config, st *store.Store, objects objs3.ObjectStore, ing *ingest.Service) *Handler {
	return &Handler{
		cfg:     cfg,
		store:   st,
		objects: objects,
		ingest:  ing,
		tus:     map[string]*tusUpload{},
	}
}

func Register(h *server.Hertz, hh *Handler) {
	h.Use(hh.CORS)
	h.POST("/functions/v1/api-key-exchange", hh.APIKeyExchange)
	h.Any("/rest/v1/job", hh.Job)
	h.Any("/rest/v1/trial", hh.Trial)
	h.POST("/rest/v1/agent", hh.Agent)
	h.POST("/rest/v1/model", hh.Model)
	h.POST("/rest/v1/trial_model", hh.TrialModel)
	h.POST("/rest/v1/rpc/list_my_orgs", hh.ListMyOrgs)
	h.POST("/rest/v1/rpc/:name", hh.RPCNotFound)
	// storage3 POST/PUT /storage/v1/object/{bucket}/{path}; path may contain slashes.
	h.POST("/storage/v1/object/*objectKey", hh.StorageUpload)
	h.PUT("/storage/v1/object/*objectKey", hh.StorageUpload)
	h.POST("/storage/v1/upload/resumable", hh.TusCreate)
	h.HEAD("/storage/v1/upload/resumable/:id", hh.TusHead)
	h.PATCH("/storage/v1/upload/resumable/:id", hh.TusPatch)
}

const corsAllowHeaders = "authorization, apikey, content-type, prefer, x-upsert, x-client-info, tus-resumable, upload-length, upload-offset, upload-metadata, accept, accept-profile, content-profile, range, range-unit, cache-control"
const corsExposeHeaders = "Location, Upload-Offset, Upload-Length, Tus-Resumable, Tus-Version, Content-Range, Range-Unit"

func harborPath(path string) bool {
	return strings.HasPrefix(path, "/functions/v1/") ||
		strings.HasPrefix(path, "/rest/v1/") ||
		strings.HasPrefix(path, "/storage/v1/")
}

func (h *Handler) CORS(ctx context.Context, c *app.RequestContext) {
	path := string(c.Path())
	if !harborPath(path) {
		c.Next(ctx)
		return
	}
	origin := string(c.GetHeader("Origin"))
	if origin == "" {
		c.Header("Access-Control-Allow-Origin", "*")
	} else {
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Vary", "Origin")
		c.Header("Access-Control-Allow-Credentials", "true")
	}
	c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, HEAD, OPTIONS, DELETE")
	c.Header("Access-Control-Allow-Headers", corsAllowHeaders)
	c.Header("Access-Control-Expose-Headers", corsExposeHeaders)
	c.Header("Access-Control-Max-Age", "86400")
	if strings.HasPrefix(path, "/storage/v1/upload/resumable") {
		c.Header("Tus-Resumable", "1.0.0")
		c.Header("Tus-Version", "1.0.0")
		c.Header("Tus-Extension", "creation,creation-with-upload")
	}
	if string(c.Method()) == "OPTIONS" {
		c.AbortWithStatus(204)
		return
	}
	c.Next(ctx)
}

func requestAPIKey(c *app.RequestContext) string {
	for _, k := range []string{"apikey", "ApiKey", "X-API-Key"} {
		if v := string(c.GetHeader(k)); v != "" {
			return v
		}
	}
	return ""
}

func (h *Handler) authError(c *app.RequestContext, status int, code, message string) {
	if strings.HasPrefix(string(c.Path()), "/storage/") {
		storageErr(c, status, message)
		return
	}
	pgrstError(c, status, code, message)
}

func (h *Handler) requireHarbor(c *app.RequestContext) (jwt.MapClaims, bool) {
	// supabase-py sends apikey on PostgREST/Storage. Harbor TUS (resumable.py)
	// sends only Authorization: Bearer <JWT> — do not require apikey when JWT is valid.
	if key := requestAPIKey(c); key != "" && key != h.cfg.AnonKey {
		h.authError(c, 401, "401", "Invalid API key")
		return nil, false
	}
	auth := string(c.GetHeader("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		h.authError(c, 401, "PGRST301", "Missing bearer token")
		return nil, false
	}
	raw := strings.TrimSpace(auth[7:])
	tok, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected jwt method")
		}
		return []byte(h.cfg.JWTSecret), nil
	})
	if err != nil || !tok.Valid {
		h.authError(c, 401, "PGRST301", "Invalid JWT")
		return nil, false
	}
	claims, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		h.authError(c, 401, "PGRST301", "Invalid JWT")
		return nil, false
	}
	return claims, true
}

func (h *Handler) sub(claims jwt.MapClaims) string {
	if v, _ := claims["sub"].(string); v != "" {
		return v
	}
	return h.cfg.UserSub
}

func (h *Handler) APIKeyExchange(ctx context.Context, c *app.RequestContext) {
	if requestAPIKey(c) != h.cfg.AnonKey {
		pgrstError(c, 401, "401", "Invalid API key")
		return
	}
	var body struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(c.Request.Body(), &body); err != nil || body.APIKey == "" {
		pgrstError(c, 400, "400", "api_key is required")
		return
	}
	if !hmac.Equal([]byte(body.APIKey), []byte(h.cfg.Token)) {
		pgrstError(c, 401, "401", "Invalid api_key")
		return
	}
	expires := h.cfg.JWTExpiresIn
	if expires <= 0 {
		expires = conf.DefaultJWTExpires
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":  h.cfg.UserSub,
		"role": "authenticated",
		"iat":  now.Unix(),
		"exp":  now.Add(time.Duration(expires) * time.Second).Unix(),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString([]byte(h.cfg.JWTSecret))
	if err != nil {
		pgrstError(c, 500, "500", "failed to mint token")
		return
	}
	c.JSON(200, map[string]any{"access_token": signed, "expires_in": expires})
}

func pgrstError(c *app.RequestContext, status int, code, message string) {
	c.JSON(status, map[string]any{
		"code":    code,
		"message": message,
		"details": nil,
		"hint":    nil,
	})
}

func preferReturn(c *app.RequestContext) string {
	p := strings.ToLower(string(c.GetHeader("Prefer")))
	if strings.Contains(p, "return=minimal") {
		return "minimal"
	}
	return "representation"
}

func preferResolution(c *app.RequestContext) string {
	p := strings.ToLower(string(c.GetHeader("Prefer")))
	if strings.Contains(p, "resolution=ignore-duplicates") {
		return "ignore"
	}
	if strings.Contains(p, "resolution=merge-duplicates") {
		return "merge"
	}
	q := string(c.Query("on_conflict"))
	_ = q
	return ""
}

func selectCols(c *app.RequestContext) []string {
	return splitSelect(string(c.Query("select")))
}

func splitSelect(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '(':
			depth++
			b.WriteRune(r)
		case ')':
			if depth > 0 {
				depth--
			}
			b.WriteRune(r)
		case ',':
			if depth == 0 {
				if p := strings.TrimSpace(b.String()); p != "" {
					out = append(out, p)
				}
				b.Reset()
				continue
			}
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	if p := strings.TrimSpace(b.String()); p != "" {
		out = append(out, p)
	}
	return out
}

func eqParam(c *app.RequestContext, key string) (string, bool) {
	v := string(c.Query(key))
	if v == "" {
		return "", false
	}
	if strings.HasPrefix(v, "eq.") {
		return strings.TrimPrefix(v, "eq."), true
	}
	return v, true
}

func maybeSingle(c *app.RequestContext) bool {
	acc := strings.ToLower(string(c.GetHeader("Accept")))
	return strings.Contains(acc, "vnd.pgrst.object") || strings.Contains(string(c.Query("limit")), "1") && strings.Contains(acc, "object")
}

func writeMaybeSingle(c *app.RequestContext, rows []map[string]any) {
	if maybeSingle(c) || (len(rows) <= 1 && strings.Contains(strings.ToLower(string(c.GetHeader("Accept"))), "vnd.pgrst.object")) {
		if len(rows) == 0 {
			c.JSON(406, map[string]any{
				"code":    "PGRST116",
				"details": "The result contains 0 rows",
				"hint":    nil,
				"message": "Cannot coerce the result to a single JSON object",
			})
			return
		}
		c.JSON(200, rows[0])
		return
	}
	if rows == nil {
		rows = []map[string]any{}
	}
	c.JSON(200, rows)
}

func parseRange(c *app.RequestContext) (offset, limit int) {
	limit = 1000
	h := string(c.GetHeader("Range"))
	h = strings.TrimPrefix(h, "items=")
	if h == "" {
		return 0, limit
	}
	parts := strings.SplitN(h, "-", 2)
	if len(parts) != 2 {
		return 0, limit
	}
	start, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	end, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || end < start {
		return 0, limit
	}
	return start, end - start + 1
}

func asText(v any) *string {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case string:
		return &t
	case json.Number:
		s := t.String()
		return &s
	default:
		b, err := json.Marshal(t)
		if err != nil {
			s := fmt.Sprint(t)
			return &s
		}
		s := string(b)
		if s == "null" {
			return nil
		}
		return &s
	}
}

func asInt64(v any) *int64 {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case float64:
		n := int64(t)
		return &n
	case json.Number:
		n, err := t.Int64()
		if err != nil {
			f, err2 := t.Float64()
			if err2 != nil {
				return nil
			}
			n = int64(f)
		}
		return &n
	case int:
		n := int64(t)
		return &n
	case int64:
		return &t
	}
	return nil
}

func asFloat(v any) *float64 {
	if v == nil {
		return nil
	}
	switch t := v.(type) {
	case float64:
		return &t
	case json.Number:
		f, err := t.Float64()
		if err != nil {
			return nil
		}
		return &f
	}
	return nil
}

func decodeJSON(body []byte) (map[string]any, error) {
	var v any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	switch t := v.(type) {
	case map[string]any:
		return t, nil
	case []any:
		if len(t) == 0 {
			return map[string]any{}, nil
		}
		if m, ok := t[0].(map[string]any); ok {
			return m, nil
		}
	}
	return nil, errors.New("expected JSON object")
}

func decodeJSONList(body []byte) ([]map[string]any, error) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, err
	}
	switch t := v.(type) {
	case map[string]any:
		return []map[string]any{t}, nil
	case []any:
		var out []map[string]any
		for _, x := range t {
			m, ok := x.(map[string]any)
			if !ok {
				return nil, errors.New("expected object")
			}
			out = append(out, m)
		}
		return out, nil
	}
	return nil, errors.New("expected JSON object or array")
}

func strID(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}
