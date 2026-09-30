package main

import (
	"bufio"
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"songbattle/config"
	"songbattle/database"
	"songbattle/importer"
	"songbattle/storage"
)

// readSources loads one source per line ("playlist:<id>" or "search:<query>"); blank lines and # comments are ignored.
func readSources(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if l := strings.TrimSpace(sc.Text()); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out, sc.Err()
}

// Usage:
//
//	go run ./cmd/importer -source "search:daft punk" -limit 30
//	go run ./cmd/importer -sources sources.txt -limit 30
func main() {
	source := flag.String("source", "", `one source: "playlist:<id>" or "search:<query>"`)
	file := flag.String("sources", "", "text file with one source per line")
	limit := flag.Int("limit", 30, "max tracks to process per source (0 = all)")
	flag.Parse()

	var sources []string
	if *source != "" {
		sources = append(sources, *source)
	}
	if *file != "" {
		more, err := readSources(*file)
		if err != nil {
			log.Fatalf("reading %s: %v", *file, err)
		}
		sources = append(sources, more...)
	}
	if len(sources) == 0 {
		log.Fatal("give -source or -sources")
	}

	cfg := config.LoadImporter()
	db, err := database.OpenPostgres(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	p, err := importer.NewProvider(cfg.MusicProvider, cfg.MusicURL, cfg.MusicKey)
	if err != nil {
		log.Fatal(err)
	}
	if !cfg.AllowAudioDownload {
		log.Println("EXTERNAL_MUSIC_ALLOW_AUDIO_DOWNLOAD is not true: importing metadata only (songs won't be playable)")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	im := &importer.Importer{DB: db, P: p, Store: storage.New(cfg.SupabaseURL, cfg.AudioBucket, cfg.ServiceKey), AllowAudio: cfg.AllowAudioDownload}

	var total importer.Stats
	failedSources := 0
	for _, src := range sources {
		if ctx.Err() != nil {
			break
		}
		log.Printf("importing %q", src)
		st, err := im.Run(ctx, src, *limit) // a failing source doesn't stop the others
		log.Printf("  seen=%d inserted=%d skipped=%d audio=%d failed=%d", st.Seen, st.Inserted, st.Skipped, st.Audio, st.Failed)
		total.Seen, total.Inserted, total.Skipped, total.Audio, total.Failed = total.Seen+st.Seen, total.Inserted+st.Inserted, total.Skipped+st.Skipped, total.Audio+st.Audio, total.Failed+st.Failed
		if err != nil {
			failedSources++
			log.Printf("  source stopped early: %v", err)
		}
	}
	log.Printf("done: seen=%d inserted=%d skipped=%d audio=%d failed=%d (failed sources: %d)", total.Seen, total.Inserted, total.Skipped, total.Audio, total.Failed, failedSources)
	if failedSources > 0 {
		os.Exit(1)
	}
}
