package static

import (
	"context"
	"embed"
	"net/http"
	"os"
	"path/filepath"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

//go:embed ui/* admin/*
var uiFS embed.FS

// Register 挂载前台 `/` 与后台 `/admin/`。dir 为空时用嵌入文件；否则从该目录读同名路径。
func Register(h *server.Hertz, dir string) {
	h.GET("/", serve(dir, "ui/index.html", "text/html; charset=utf-8"))
	h.GET("/app.css", serve(dir, "ui/app.css", "text/css; charset=utf-8"))
	h.GET("/app.js", serve(dir, "ui/app.js", "application/javascript; charset=utf-8"))
	h.GET("/admin", serve(dir, "admin/index.html", "text/html; charset=utf-8"))
	h.GET("/admin/", serve(dir, "admin/index.html", "text/html; charset=utf-8"))
	h.GET("/admin/app.css", serve(dir, "admin/app.css", "text/css; charset=utf-8"))
	h.GET("/admin/app.js", serve(dir, "admin/app.js", "application/javascript; charset=utf-8"))
}

func serve(dir, name, contentType string) app.HandlerFunc {
	embedded, embedErr := uiFS.ReadFile(name)
	return func(ctx context.Context, c *app.RequestContext) {
		data, err := embedded, embedErr
		if dir != "" {
			data, err = os.ReadFile(filepath.Join(dir, filepath.FromSlash(name)))
		}
		if err != nil {
			c.AbortWithStatus(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, contentType, data)
	}
}
