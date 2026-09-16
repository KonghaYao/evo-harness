package ingest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
	"strings"
)

func PackTarGz(files map[string][]byte) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		name = path.Clean(strings.ReplaceAll(name, "\\", "/"))
		name = strings.TrimPrefix(name, "/")
		if name == "." || name == "" {
			continue
		}
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(body); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func UnpackTarGz(r io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		name := path.Clean(strings.ReplaceAll(hdr.Name, "\\", "/"))
		name = strings.TrimPrefix(name, "/")
		if name == "." || name == "" || strings.Contains(name, "..") {
			return nil, fmt.Errorf("unsafe tar path %q", hdr.Name)
		}
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, tr); err != nil {
			return nil, err
		}
		out[name] = buf.Bytes()
	}
	return out, nil
}

func StripCommonRoot(files map[string][]byte) (root string, stripped map[string][]byte) {
	if len(files) == 0 {
		return "", files
	}
	var rootName string
	for name := range files {
		parts := strings.Split(name, "/")
		if len(parts) < 2 {
			return "", files
		}
		if rootName == "" {
			rootName = parts[0]
		} else if parts[0] != rootName {
			return "", files
		}
	}
	out := make(map[string][]byte, len(files))
	for name, body := range files {
		rest := strings.TrimPrefix(name, rootName+"/")
		if rest == "" || rest == name {
			continue
		}
		out[rest] = body
	}
	return rootName, out
}
