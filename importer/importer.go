package importer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"songbattle/models"
	"songbattle/storage"

	"gorm.io/gorm"
)

type Importer struct {
	DB         *gorm.DB
	P          Provider
	Store      *storage.Client
	AllowAudio bool // only true when the provider's terms permit downloading/storing audio
}

type Stats struct{ Seen, Inserted, Skipped, Audio, Failed int }

// opaquePath never contains title/artist: random hex only.
func opaquePath() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b) + ".mp3", nil
}

// Run is idempotent: existing (provider, provider_song_id) rows are skipped, and per-track failures don't stop the run.
func (im *Importer) Run(ctx context.Context, source string, limit int) (Stats, error) {
	var st Stats
	err := im.P.Tracks(ctx, source, limit, func(t Track) error {
		st.Seen++
		if err := im.one(ctx, t, &st); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			st.Failed++
			log.Printf("track %s (%s): %v", t.ProviderID, t.Title, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond): // gentle on provider rate limits
			return nil
		}
	})
	return st, err
}

func (im *Importer) one(ctx context.Context, t Track, st *Stats) error {
	s, ok := ToSong(im.P.Name(), t)
	if !ok {
		return errors.New("missing id/title/artist")
	}
	var ex models.Song
	err := im.DB.WithContext(ctx).Where("provider = ? AND provider_song_id = ?", s.Provider, s.ProviderSongID).First(&ex).Error
	switch {
	case err == nil: // already known: only retry a missing audio file
		if ex.AudioPath != "" || !im.AllowAudio || t.PreviewURL == "" {
			st.Skipped++
			return nil
		}
		s = ex
	case errors.Is(err, gorm.ErrRecordNotFound):
		if err := im.DB.WithContext(ctx).Create(&s).Error; err != nil {
			return err
		}
		st.Inserted++
	default:
		return err
	}
	if !im.AllowAudio || t.PreviewURL == "" {
		return nil // metadata-only import
	}
	data, ct, err := im.P.Fetch(ctx, t.PreviewURL)
	if err != nil {
		return fmt.Errorf("download audio: %w", err)
	}
	if !strings.HasPrefix(ct, "audio/") && ct != "application/octet-stream" {
		return fmt.Errorf("unexpected content-type %q", ct)
	}
	path, err := opaquePath()
	if err != nil {
		return err
	}
	if err := im.Store.Upload(ctx, path, "audio/mpeg", data); err != nil {
		return err
	}
	if err := im.DB.WithContext(ctx).Model(&models.Song{}).Where("id = ?", s.ID).Update("audio_path", path).Error; err != nil {
		return err
	}
	st.Audio++
	return nil
}
