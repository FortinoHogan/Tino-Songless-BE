package config

import (
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

type Config struct {
	DatabaseURL, RedisURL, Port, JWTSecret, CORSOrigin  string
	SupabaseURL, ServiceKey, AudioBucket                string
	RoundSeconds, RevealSeconds, MinPlayers, MaxPlayers int
	// Importer-only. The game server never reads or uses these.
	MusicURL, MusicKey, MusicProvider string
	AllowAudioDownload                bool
}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
func num(k string, def int) int {
	if n, err := strconv.Atoi(env(k, "")); err == nil && n > 0 {
		return n
	}
	return def
}
func require(vals map[string]string) {
	for k, v := range vals {
		if v == "" {
			log.Fatalf("missing required env var %s", k)
		}
	}
}

func load() Config {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file, using process environment")
	}
	return Config{
		DatabaseURL: env("DATABASE_URL", ""), RedisURL: env("REDIS_URL", ""), Port: env("PORT", "8080"),
		JWTSecret: env("JWT_SECRET", ""), CORSOrigin: env("CORS_ORIGIN", "http://localhost:5173"),
		SupabaseURL: env("SUPABASE_URL", ""), ServiceKey: env("SUPABASE_SECRET_KEY", ""), AudioBucket: env("AUDIO_STORAGE_BUCKET", "Songs"),
		RoundSeconds: num("ROUND_SECONDS", 10), RevealSeconds: num("REVEAL_SECONDS", 8),
		MinPlayers: num("MIN_PLAYERS", 2), MaxPlayers: num("MAX_PLAYERS", 12),
		MusicURL: env("EXTERNAL_MUSIC_API_URL", ""), MusicKey: env("EXTERNAL_MUSIC_API_KEY", ""),
		MusicProvider:      strings.ToLower(env("EXTERNAL_MUSIC_API_PROVIDER", "")),
		AllowAudioDownload: env("EXTERNAL_MUSIC_ALLOW_AUDIO_DOWNLOAD", "false") == "true",
	}
}

// Load is for the game server: no external music API settings required.
func Load() Config {
	c := load()
	require(map[string]string{"DATABASE_URL": c.DatabaseURL, "REDIS_URL": c.RedisURL, "JWT_SECRET": c.JWTSecret,
		"SUPABASE_URL": c.SupabaseURL, "SUPABASE_SECRET_KEY": c.ServiceKey})
	return c
}

// LoadImporter is for cmd/importer: needs the database, storage and the external API, but not Redis/JWT.
func LoadImporter() Config {
	c := load()
	require(map[string]string{"DATABASE_URL": c.DatabaseURL, "SUPABASE_URL": c.SupabaseURL, "SUPABASE_SECRET_KEY": c.ServiceKey,
		"EXTERNAL_MUSIC_API_URL": c.MusicURL, "EXTERNAL_MUSIC_API_PROVIDER": c.MusicProvider})
	return c
}
