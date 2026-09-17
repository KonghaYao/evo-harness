package harborcompat

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/google/uuid"
)

func (h *Handler) objectName(c *app.RequestContext) string {
	if n := string(c.Query("name")); n != "" {
		return strings.TrimPrefix(n, "/")
	}
	p := string(c.Request.URI().Path())
	const prefix = "/storage/v1/object/"
	rest := strings.TrimPrefix(p, prefix)
	rest = strings.TrimPrefix(rest, "/")
	if i := strings.Index(rest, "/"); i >= 0 {
		// bucket is rest[:i]; Harbor uses "results"
		return rest[i+1:]
	}
	n := strings.TrimPrefix(c.Param("objectKey"), "/")
	if n == "" {
		n = strings.TrimPrefix(c.Param("objectName"), "/")
	}
	if i := strings.Index(n, "/"); i >= 0 && (strings.HasPrefix(n, "results/") || strings.HasPrefix(n, "packages/")) {
		return n[i+1:]
	}
	return n
}

func (h *Handler) readObjectBody(c *app.RequestContext) ([]byte, error) {
	ct := strings.ToLower(string(c.GetHeader("Content-Type")))
	if strings.Contains(ct, "multipart/form-data") {
		fh, err := c.FormFile("file")
		if err == nil && fh != nil {
			f, err := fh.Open()
			if err != nil {
				return nil, err
			}
			defer f.Close()
			return io.ReadAll(f)
		}
	}
	return append([]byte(nil), c.Request.Body()...), nil
}

func (h *Handler) StorageUpload(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	name := h.objectName(c)
	if name == "" {
		storageErr(c, 400, "objectName is required")
		return
	}
	body, err := h.readObjectBody(c)
	if err != nil {
		storageErr(c, 400, err.Error())
		return
	}
	if int64(len(body)) > h.cfg.MaxUploadBytes {
		storageErr(c, 413, "payload too large")
		return
	}
	exists, _, err := h.objects.Head(ctx, name)
	if err != nil {
		storageErr(c, 500, err.Error())
		return
	}
	upsert := strings.EqualFold(string(c.GetHeader("x-upsert")), "true")
	if exists && !upsert {
		c.JSON(409, map[string]any{"statusCode": "409", "error": "Duplicate", "message": "The resource already exists"})
		return
	}
	ct := string(c.GetHeader("Content-Type"))
	if err := h.objects.Put(ctx, name, bytes.NewReader(body), int64(len(body)), ct); err != nil {
		storageErr(c, 500, err.Error())
		return
	}
	c.JSON(200, map[string]any{"Key": "results/" + name, "Id": name})
}

func storageErr(c *app.RequestContext, status int, msg string) {
	c.JSON(status, map[string]any{"statusCode": strconv.Itoa(status), "error": msg, "message": msg})
}

func (h *Handler) TusCreate(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	if string(c.GetHeader("Tus-Resumable")) == "" {
		c.Header("Tus-Resumable", "1.0.0")
	} else {
		c.Header("Tus-Resumable", "1.0.0")
	}
	meta := parseTusMetadata(string(c.GetHeader("Upload-Metadata")))
	objectName := strings.TrimPrefix(meta["objectName"], "/")
	if objectName == "" {
		storageErr(c, 400, "objectName metadata required")
		return
	}
	bucket := meta["bucketName"]
	if bucket == "" {
		bucket = "results"
	}
	lengthStr := string(c.GetHeader("Upload-Length"))
	length, err := strconv.ParseInt(lengthStr, 10, 64)
	if err != nil || length < 0 {
		storageErr(c, 400, "Upload-Length required")
		return
	}
	if length > h.cfg.MaxUploadBytes {
		storageErr(c, 413, "payload too large")
		return
	}
	exists, _, err := h.objects.Head(ctx, objectName)
	if err != nil {
		storageErr(c, 500, err.Error())
		return
	}
	if exists {
		c.JSON(409, map[string]any{"statusCode": "409", "error": "Duplicate", "message": "The resource already exists"})
		return
	}
	id := uuid.NewString()
	up := &tusUpload{
		ID:          id,
		ObjectName:  objectName,
		Bucket:      bucket,
		Length:      length,
		ContentType: meta["contentType"],
		Buf:         make([]byte, 0, min64(length, 1<<20)),
	}
	body := c.Request.Body()
	if len(body) > 0 {
		if int64(len(body)) > length {
			storageErr(c, 400, "body exceeds Upload-Length")
			return
		}
		up.Buf = append(up.Buf, body...)
		up.Offset = int64(len(up.Buf))
	}
	h.tusMu.Lock()
	h.tus[id] = up
	h.tusMu.Unlock()
	if up.Offset == up.Length && up.Length > 0 {
		if err := h.finishTus(ctx, up); err != nil {
			storageErr(c, 500, err.Error())
			return
		}
	}
	loc := tusLocation(c, id)
	c.Header("Location", loc)
	c.Header("Tus-Resumable", "1.0.0")
	c.Header("Upload-Offset", strconv.FormatInt(up.Offset, 10))
	c.Status(201)
}

func tusLocation(c *app.RequestContext, id string) string {
	// Relative Location: Harbor CLI joins against HARBOR_SUPABASE_URL and
	// rejects a Location whose origin differs (resumable._validate_tus_url).
	return "/storage/v1/upload/resumable/" + id
}

func (h *Handler) TusHead(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	id := c.Param("id")
	h.tusMu.Lock()
	up := h.tus[id]
	h.tusMu.Unlock()
	c.Header("Tus-Resumable", "1.0.0")
	c.Header("Cache-Control", "no-store")
	if up == nil {
		c.Status(404)
		return
	}
	c.Header("Upload-Offset", strconv.FormatInt(up.Offset, 10))
	c.Header("Upload-Length", strconv.FormatInt(up.Length, 10))
	c.Status(200)
}

func (h *Handler) TusPatch(ctx context.Context, c *app.RequestContext) {
	if _, ok := h.requireHarbor(c); !ok {
		return
	}
	c.Header("Tus-Resumable", "1.0.0")
	id := c.Param("id")
	h.tusMu.Lock()
	up := h.tus[id]
	if up == nil {
		h.tusMu.Unlock()
		c.Status(404)
		return
	}
	offHdr := string(c.GetHeader("Upload-Offset"))
	off, err := strconv.ParseInt(offHdr, 10, 64)
	if err != nil || off != up.Offset {
		h.tusMu.Unlock()
		c.Status(409)
		return
	}
	chunk := c.Request.Body()
	if up.Offset+int64(len(chunk)) > up.Length {
		h.tusMu.Unlock()
		storageErr(c, 400, "chunk exceeds Upload-Length")
		return
	}
	up.Buf = append(up.Buf, chunk...)
	up.Offset += int64(len(chunk))
	done := up.Offset == up.Length
	cp := up
	h.tusMu.Unlock()
	if done {
		if err := h.finishTus(ctx, cp); err != nil {
			storageErr(c, 500, err.Error())
			return
		}
	}
	c.Header("Upload-Offset", strconv.FormatInt(cp.Offset, 10))
	c.Status(204)
}

func (h *Handler) finishTus(ctx context.Context, up *tusUpload) error {
	if err := h.objects.Put(ctx, up.ObjectName, bytes.NewReader(up.Buf), int64(len(up.Buf)), up.ContentType); err != nil {
		return err
	}
	h.tusMu.Lock()
	delete(h.tus, up.ID)
	h.tusMu.Unlock()
	return nil
}

func parseTusMetadata(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	parts := strings.Split(s, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, " ", 2)
		key := kv[0]
		if len(kv) == 1 {
			out[key] = ""
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(kv[1])
		if err != nil {
			out[key] = kv[1]
			continue
		}
		out[key] = string(raw)
	}
	return out
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
