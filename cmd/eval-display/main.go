package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"evo-harness/internal/evaldisplay/conf"
	"evo-harness/internal/evaldisplay/httpapi"
	"evo-harness/internal/evaldisplay/ingest"
	"evo-harness/internal/evaldisplay/overlay"
	"evo-harness/internal/evaldisplay/query"
	objs3 "evo-harness/internal/evaldisplay/s3"
	"evo-harness/internal/evaldisplay/store"
)

func main() {
	cfg := conf.FromEnv()
	if err := cfg.Validate(); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SQLitePath), 0o755); err != nil && filepath.Dir(cfg.SQLitePath) != "." {
		log.Fatal(err)
	}
	ctx := context.Background()
	st, err := store.Open(cfg.SQLitePath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	objects, err := objs3.New(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	h := httpapi.NewServer(httpapi.Deps{
		Conf:    cfg,
		Store:   st,
		Objects: objects,
		Query:   query.New(st),
		Overlay: overlay.New(st),
		Ingest:  ingest.New(st, objects),
	})
	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
		<-ch
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	}()
	log.Printf("fenix-eval listening on %s", cfg.Addr)
	h.Spin()
}
