package conf

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultAddr           = ":8080"
	DefaultSQLitePath     = "./data/eval-display.sqlite"
	DefaultJWTExpires     = 900
	DefaultMaxUploadBytes = 512 << 20
	DefaultUserSub        = "11111111-1111-4111-8111-111111111111"
	DefaultOrgID          = "22222222-2222-4222-8222-222222222222"
	DefaultOrgName        = "local"
)

// Config is loaded from environment variables. Tests construct it directly.
type Config struct {
	Addr           string
	SQLitePath     string
	Token          string
	AdminToken     string
	AnonKey        string
	JWTSecret      string
	JWTExpiresIn   int
	UserSub        string
	OrgID          string
	OrgName        string
	MaxUploadBytes int64

	S3Backend   string // memory | fs | aws
	S3Dir       string
	S3Endpoint  string
	S3Bucket    string
	S3Region    string
	S3AccessKey string
	S3SecretKey string
	S3SSE       string
	S3PathStyle bool

	StaticDir string
}

func FromEnv() Config {
	maxUpload := int64(DefaultMaxUploadBytes)
	if v := os.Getenv("EVAL_DISPLAY_MAX_UPLOAD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxUpload = n
		}
	}
	expires := DefaultJWTExpires
	if v := os.Getenv("EVAL_DISPLAY_JWT_EXPIRES_IN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			expires = n
		}
	}
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("EVAL_DISPLAY_S3_BACKEND")))
	dir := os.Getenv("EVAL_DISPLAY_S3_DIR")
	endpoint := os.Getenv("EVAL_DISPLAY_S3_ENDPOINT")
	bucket := os.Getenv("EVAL_DISPLAY_S3_BUCKET")
	if backend == "" {
		switch {
		case dir != "":
			backend = "fs"
		case endpoint != "" || bucket != "":
			backend = "aws"
		default:
			backend = "memory"
		}
	}
	pathStyle := true
	if v := os.Getenv("EVAL_DISPLAY_S3_PATH_STYLE"); v != "" {
		pathStyle = v == "1" || strings.EqualFold(v, "true")
	}
	return Config{
		Addr:           envOr("EVAL_DISPLAY_ADDR", DefaultAddr),
		SQLitePath:     envOr("EVAL_DISPLAY_SQLITE_PATH", DefaultSQLitePath),
		Token:          os.Getenv("EVAL_DISPLAY_TOKEN"),
		AdminToken:     os.Getenv("EVAL_DISPLAY_ADMIN_TOKEN"),
		AnonKey:        os.Getenv("EVAL_DISPLAY_ANON_KEY"),
		JWTSecret:      os.Getenv("EVAL_DISPLAY_JWT_SECRET"),
		JWTExpiresIn:   expires,
		UserSub:        envOr("EVAL_DISPLAY_USER_SUB", DefaultUserSub),
		OrgID:          envOr("EVAL_DISPLAY_ORG_ID", DefaultOrgID),
		OrgName:        envOr("EVAL_DISPLAY_ORG_NAME", DefaultOrgName),
		MaxUploadBytes: maxUpload,
		S3Backend:      backend,
		S3Dir:          dir,
		S3Endpoint:     endpoint,
		S3Bucket:       envOr("EVAL_DISPLAY_S3_BUCKET", "results"),
		S3Region:       envOr("EVAL_DISPLAY_S3_REGION", envOr("AWS_REGION", "us-east-1")),
		S3AccessKey:    envOr("EVAL_DISPLAY_S3_ACCESS_KEY", os.Getenv("AWS_ACCESS_KEY_ID")),
		S3SecretKey:    envOr("EVAL_DISPLAY_S3_SECRET_KEY", os.Getenv("AWS_SECRET_ACCESS_KEY")),
		S3SSE:          os.Getenv("EVAL_DISPLAY_S3_SSE"),
		S3PathStyle:    pathStyle,
		StaticDir:      os.Getenv("EVAL_DISPLAY_STATIC_DIR"),
	}
}

func (c Config) Validate() error {
	if strings.TrimSpace(c.AdminToken) == "" {
		return fmt.Errorf("EVAL_DISPLAY_ADMIN_TOKEN is required")
	}
	if strings.TrimSpace(c.AnonKey) == "" {
		return fmt.Errorf("EVAL_DISPLAY_ANON_KEY is required")
	}
	if strings.TrimSpace(c.JWTSecret) == "" {
		return fmt.Errorf("EVAL_DISPLAY_JWT_SECRET is required")
	}
	if c.JWTExpiresIn <= 0 {
		return fmt.Errorf("EVAL_DISPLAY_JWT_EXPIRES_IN must be positive")
	}
	return nil
}

func (c Config) JWTExpiry() time.Duration {
	return time.Duration(c.JWTExpiresIn) * time.Second
}

// AdminBearer 后台令牌。前台读接口公开，不使用 EVAL_DISPLAY_TOKEN。
func (c Config) AdminBearer() string {
	return strings.TrimSpace(c.AdminToken)
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
