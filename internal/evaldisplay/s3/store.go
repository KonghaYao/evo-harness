package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"evo-harness/internal/evaldisplay/conf"

	aws "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

var ErrNotFound = errors.New("s3 object not found")

// ObjectStore is the job-tree blob store. Production uses AWS/MinIO;
// tests may use the memory or filesystem implementations.
type ObjectStore interface {
	Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
	Head(ctx context.Context, key string) (exists bool, size int64, err error)
	DeletePrefix(ctx context.Context, prefix string) error
}

func New(ctx context.Context, cfg conf.Config) (ObjectStore, error) {
	switch strings.ToLower(cfg.S3Backend) {
	case "", "memory":
		return NewMemory(), nil
	case "fs", "filesystem", "dir":
		if cfg.S3Dir == "" {
			return nil, fmt.Errorf("EVAL_DISPLAY_S3_DIR is required for fs backend")
		}
		return NewFS(cfg.S3Dir)
	case "aws", "s3", "minio":
		return NewAWS(ctx, cfg)
	default:
		return nil, fmt.Errorf("unknown EVAL_DISPLAY_S3_BACKEND %q", cfg.S3Backend)
	}
}

type Memory struct {
	mu      sync.RWMutex
	objects map[string][]byte
	types   map[string]string
}

func NewMemory() *Memory {
	return &Memory{objects: map[string][]byte{}, types: map[string]string{}}
}

func (m *Memory) Put(_ context.Context, key string, body io.Reader, size int64, contentType string) error {
	key = strings.TrimPrefix(key, "/")
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	if size >= 0 && int64(len(b)) != size && size != 0 {
		// size 0 means unknown; otherwise prefer actual bytes
	}
	cp := append([]byte(nil), b...)
	m.mu.Lock()
	m.objects[key] = cp
	if contentType != "" {
		m.types[key] = contentType
	}
	m.mu.Unlock()
	return nil
}

func (m *Memory) Get(_ context.Context, key string) (io.ReadCloser, error) {
	key = strings.TrimPrefix(key, "/")
	m.mu.RLock()
	b, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

func (m *Memory) Head(_ context.Context, key string) (bool, int64, error) {
	key = strings.TrimPrefix(key, "/")
	m.mu.RLock()
	b, ok := m.objects[key]
	m.mu.RUnlock()
	if !ok {
		return false, 0, nil
	}
	return true, int64(len(b)), nil
}

func (m *Memory) DeletePrefix(_ context.Context, prefix string) error {
	prefix = strings.TrimPrefix(prefix, "/")
	if prefix == "" {
		return fmt.Errorf("empty s3 prefix")
	}
	exact := strings.TrimSuffix(prefix, "/")
	m.mu.Lock()
	defer m.mu.Unlock()
	for k := range m.objects {
		if k == exact || strings.HasPrefix(k, prefix) || (exact != "" && strings.HasPrefix(k, exact+"/")) {
			delete(m.objects, k)
			delete(m.types, k)
		}
	}
	return nil
}

func (m *Memory) Bytes(key string) []byte {
	key = strings.TrimPrefix(key, "/")
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]byte(nil), m.objects[key]...)
}

type FS struct {
	root string
}

func NewFS(root string) (*FS, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &FS{root: abs}, nil
}

func (f *FS) path(key string) (string, error) {
	key = strings.TrimPrefix(filepath.ToSlash(key), "/")
	if key == "" || strings.Contains(key, "..") {
		return "", fmt.Errorf("invalid object key")
	}
	p := filepath.Join(f.root, filepath.FromSlash(key))
	rel, err := filepath.Rel(f.root, p)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("invalid object key")
	}
	return p, nil
}

func (f *FS) Put(_ context.Context, key string, body io.Reader, _ int64, _ string) error {
	p, err := f.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp := p + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	return os.Rename(tmp, p)
}

func (f *FS) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := f.path(key)
	if err != nil {
		return nil, err
	}
	fh, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	return fh, err
}

func (f *FS) Head(_ context.Context, key string) (bool, int64, error) {
	p, err := f.path(key)
	if err != nil {
		return false, 0, err
	}
	st, err := os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	return true, st.Size(), nil
}

func (f *FS) DeletePrefix(_ context.Context, prefix string) error {
	prefix = strings.TrimPrefix(filepath.ToSlash(prefix), "/")
	if prefix == "" || strings.Contains(prefix, "..") {
		return fmt.Errorf("invalid object prefix")
	}
	root, err := f.path(strings.TrimSuffix(prefix, "/"))
	if err != nil {
		return err
	}
	_ = os.RemoveAll(root)
	return nil
}

type AWS struct {
	client *awss3.Client
	bucket string
	sse    string
}

func NewAWS(ctx context.Context, cfg conf.Config) (*AWS, error) {
	if cfg.S3Bucket == "" {
		return nil, fmt.Errorf("EVAL_DISPLAY_S3_BUCKET is required")
	}
	loadOpts := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(cfg.S3Region),
	}
	if cfg.S3AccessKey != "" {
		loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(cfg.S3AccessKey, cfg.S3SecretKey, ""),
		))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
	if err != nil {
		return nil, err
	}
	client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		if cfg.S3Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.S3Endpoint)
		}
		o.UsePathStyle = cfg.S3PathStyle
	})
	return &AWS{client: client, bucket: cfg.S3Bucket, sse: cfg.S3SSE}, nil
}

func (a *AWS) Put(ctx context.Context, key string, body io.Reader, size int64, contentType string) error {
	key = strings.TrimPrefix(key, "/")
	in := &awss3.PutObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
		Body:   body,
	}
	if contentType != "" {
		in.ContentType = aws.String(contentType)
	}
	if size > 0 {
		in.ContentLength = aws.Int64(size)
	}
	switch a.sse {
	case "":
	case "AES256":
		in.ServerSideEncryption = types.ServerSideEncryptionAes256
	case "aws:kms":
		in.ServerSideEncryption = types.ServerSideEncryptionAwsKms
	default:
		return fmt.Errorf("unsupported EVAL_DISPLAY_S3_SSE %q", a.sse)
	}
	_, err := a.client.PutObject(ctx, in)
	return err
}

func (a *AWS) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	key = strings.TrimPrefix(key, "/")
	out, err := a.client.GetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NoSuchKey
		if errors.As(err, &nsk) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return out.Body, nil
}

func (a *AWS) Head(ctx context.Context, key string) (bool, int64, error) {
	key = strings.TrimPrefix(key, "/")
	out, err := a.client.HeadObject(ctx, &awss3.HeadObjectInput{
		Bucket: aws.String(a.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		var nsk *types.NotFound
		if errors.As(err, &nsk) {
			return false, 0, nil
		}
		msg := strings.ToLower(err.Error())
		if strings.Contains(msg, "not found") || strings.Contains(msg, "nosuchkey") || strings.Contains(msg, "404") {
			return false, 0, nil
		}
		return false, 0, err
	}
	var size int64
	if out.ContentLength != nil {
		size = *out.ContentLength
	}
	return true, size, nil
}

func (a *AWS) DeletePrefix(ctx context.Context, prefix string) error {
	prefix = strings.TrimPrefix(prefix, "/")
	if prefix == "" {
		return fmt.Errorf("empty s3 prefix")
	}
	var token *string
	for {
		out, err := a.client.ListObjectsV2(ctx, &awss3.ListObjectsV2Input{
			Bucket:            aws.String(a.bucket),
			Prefix:            aws.String(prefix),
			ContinuationToken: token,
		})
		if err != nil {
			return err
		}
		if len(out.Contents) == 0 {
			if out.IsTruncated == nil || !*out.IsTruncated {
				return nil
			}
		} else {
			objs := make([]types.ObjectIdentifier, 0, len(out.Contents))
			for _, obj := range out.Contents {
				if obj.Key != nil {
					objs = append(objs, types.ObjectIdentifier{Key: obj.Key})
				}
			}
			if len(objs) > 0 {
				if _, err := a.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{
					Bucket: aws.String(a.bucket),
					Delete: &types.Delete{Objects: objs, Quiet: aws.Bool(true)},
				}); err != nil {
					return err
				}
			}
		}
		if out.IsTruncated == nil || !*out.IsTruncated {
			return nil
		}
		token = out.NextContinuationToken
	}
}
