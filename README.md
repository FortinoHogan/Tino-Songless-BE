# Song Battle backend (Go + Gin + GORM + Redis)
`cp .env.example .env && go mod tidy && go run ./cmd/server`

Notes / assumptions
- Live rooms (players, current round, private answer, timers) are held in one server process under per-game mutexes; Redis holds room-code -> game and the expiring opaque audio tokens. Postgres stores games/rounds/guesses/scores. Single instance only; games in progress are lost on restart.
- `games` has no room-code/host/total-rounds columns, so those live in memory/Redis. Status values written: lobby, in_progress, finished.
- Identity: JWT (HS256). `POST /api/games` and `/join` accept `displayName` and, without an Authorization header, create a guest user and return `{gameId, playerId, token}` (this matches the React client). With a header, the token's user is used and body `userId` is ignored.
- WebSocket: `/ws/games/:gameId?token=` (also `/ws?gameId=&token=`). Events carry a per-game `seq`.
- Audio is proxied from `AUDIO_STORAGE_URL/BUCKET/audio_path`, so paths never reach the browser. IDs are numeric (uint) and serialized as strings.
- Points/correctness stay hidden until `round_result`; `player_submitted` only carries counts.
- Not included: rematch in place ("Play Again" needs a new game), repositories layer (queries live in the service).

## Song catalog importer (separate from the game)
`go run ./cmd/importer -source playlist:<deezer-playlist-id> -limit 100` (or `-source "search:daft punk"`).
- Idempotent: dedupes on (provider, provider_song_id); re-running retries only songs missing audio. Optional safety net: `create unique index if not exists songs_provider_uid on songs(provider, provider_song_id);`
- Audio is downloaded only when `EXTERNAL_MUSIC_ALLOW_AUDIO_DOWNLOAD=true`; stored in the private bucket under random hex names (`<32 hex>.mp3`), and only that path is saved in `songs.audio_path`.
- The game server never imports `importer` or reads EXTERNAL_* vars; it streams audio from the private bucket with the service-role key. Keep that key server-side.
- Create the `songs` bucket in Supabase as **private**.
