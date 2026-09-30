package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"songbattle/apperr"
	"songbattle/config"
	"songbattle/models"
	"songbattle/utils"
	"songbattle/ws"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type M = map[string]any

type player struct {
	ID        uint
	Name      string
	Score     int
	Connected bool
}

// roundState holds the PRIVATE answer. It only ever lives in server memory (and songs table); it is never serialized to clients before round_result.
type roundState struct {
	ID                uint
	Num               int
	Answer            models.Song
	StartedAt, EndsAt time.Time
	Token             string
	Guesses           map[uint]string // last guess text per player
	Solved            map[uint]bool
	Wrong             map[uint]int
	Pts               map[uint]int
	ending            bool
	timer             *time.Timer
}

// live is one active room. Every field is guarded by mu; all transitions (join, guess, round end) take it.
type live struct {
	mu        sync.Mutex
	ID        uint
	Code      string
	HostID    uint
	Total     int
	RoundSecs int
	RoundNum  int
	Status    string // lobby | playing | finished
	Players   map[uint]*player
	Order     []uint
	Round     *roundState
	Used      map[uint]bool
	Last      M
	Seq       int64
}

type Service struct {
	DB    *gorm.DB
	RDB   *redis.Client
	Hub   *ws.Hub
	Cfg   config.Config
	mu    sync.RWMutex
	games map[uint]*live
}

func NewService(db *gorm.DB, rdb *redis.Client, hub *ws.Hub, cfg config.Config) *Service {
	s := &Service{DB: db, RDB: rdb, Hub: hub, Cfg: cfg, games: map[uint]*live{}}
	hub.OnPresence = s.presence
	return s
}

func sid(u uint) string     { return strconv.FormatUint(uint64(u), 10) }
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (s *Service) get(id uint) (*live, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if g := s.games[id]; g != nil {
		return g, nil
	}
	return nil, apperr.GameNotFound
}

// emit broadcasts an event with a per-game monotonic seq. Caller must hold g.mu.
func (g *live) emit(s *Service, t string, d any) {
	g.Seq++
	b, _ := json.Marshal(M{"type": t, "seq": g.Seq, "data": d})
	s.Hub.Broadcast(g.ID, b)
}

func (g *live) sorted() []*player {
	ps := make([]*player, 0, len(g.Order))
	for _, id := range g.Order {
		ps = append(ps, g.Players[id])
	}
	sort.SliceStable(ps, func(i, j int) bool { return ps[i].Score > ps[j].Score })
	return ps
}

func (g *live) scores() []M {
	out := []M{}
	for _, p := range g.sorted() {
		out = append(out, M{"playerId": sid(p.ID), "name": p.Name, "score": p.Score})
	}
	return out
}

func (g *live) view() M {
	ps := []M{}
	for _, id := range g.Order {
		p := g.Players[id]
		ps = append(ps, M{"id": sid(id), "name": p.Name, "isHost": id == g.HostID, "connected": p.Connected, "score": p.Score})
	}
	return M{"gameId": sid(g.ID), "code": g.Code, "status": g.Status, "totalRounds": g.Total, "roundSeconds": g.RoundSecs, "hostId": sid(g.HostID), "players": ps}
}

// roundView is the ONLY round payload clients get while the round is live: no song data.
func (g *live) roundView(rs *roundState) M {
	return M{"roundId": sid(rs.ID), "roundNumber": rs.Num, "totalRounds": g.Total, "audioToken": rs.Token,
		"duration": int(rs.EndsAt.Sub(rs.StartedAt).Seconds()), "startedAt": ts(rs.StartedAt), "serverTime": ts(time.Now())}
}

func (s *Service) GuestUser(name string) (uint, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 24 {
		return 0, apperr.BadRequest
	}
	sfx, err := utils.RandToken("", 6)
	if err != nil {
		return 0, err
	}
	u := models.User{Username: "guest_" + sfx, DisplayName: name}
	return u.ID, s.DB.Create(&u).Error
}

func (s *Service) Create(ctx context.Context, uid uint, groupID *uint, mode string, total int) (uint, string, error) {
	if total == 0 {
		total = 10
	}
	if total < 1 || total > 50 {
		return 0, "", apperr.BadRequest
	}
	if mode == "" {
		mode = "classic"
	}
	var u models.User
	if err := s.DB.First(&u, uid).Error; err != nil {
		return 0, "", apperr.Unauthorized
	}
	gm := models.Game{GroupID: groupID, Status: "lobby", GameMode: mode}
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&gm).Error; err != nil {
			return err
		}
		return tx.Create(&models.GamePlayer{GameID: gm.ID, UserID: uid}).Error
	}); err != nil {
		return 0, "", err
	}
	code := ""
	for i := 0; i < 10 && code == ""; i++ {
		c := utils.RoomCode()
		ok, err := s.RDB.SetNX(ctx, "room:"+c, gm.ID, 24*time.Hour).Result()
		if err != nil {
			return 0, "", err
		}
		if ok {
			code = c
		}
	}
	if code == "" {
		return 0, "", errors.New("could not allocate room code")
	}
	g := &live{ID: gm.ID, Code: code, HostID: uid, Total: total, RoundSecs: s.Cfg.RoundSeconds, Status: "lobby", Used: map[uint]bool{},
		Players: map[uint]*player{uid: {ID: uid, Name: u.DisplayName}}, Order: []uint{uid}}
	s.mu.Lock()
	s.games[gm.ID] = g
	s.mu.Unlock()
	return gm.ID, code, nil
}

func (s *Service) Join(ctx context.Context, code string, uid uint) (uint, error) {
	id, err := s.RDB.Get(ctx, "room:"+strings.ToUpper(strings.TrimSpace(code))).Uint64()
	if err != nil {
		return 0, apperr.InvalidRoom
	}
	g, err := s.get(uint(id))
	if err != nil {
		return 0, apperr.InvalidRoom
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Players[uid] != nil { // reconnect / refresh
		return g.ID, nil
	}
	switch g.Status {
	case "playing":
		return 0, apperr.AlreadyStarted
	case "finished":
		return 0, apperr.AlreadyFinished
	}
	if len(g.Players) >= s.Cfg.MaxPlayers {
		return 0, apperr.RoomFull
	}
	var u models.User
	if err := s.DB.First(&u, uid).Error; err != nil {
		return 0, apperr.Unauthorized
	}
	for _, p := range g.Players {
		if strings.EqualFold(p.Name, u.DisplayName) {
			return 0, apperr.NameTaken
		}
	}
	if err := s.DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&models.GamePlayer{GameID: g.ID, UserID: uid}).Error; err != nil {
		return 0, err
	}
	g.Players[uid] = &player{ID: uid, Name: u.DisplayName}
	g.Order = append(g.Order, uid)
	pv := M{"id": sid(uid), "name": u.DisplayName, "isHost": false, "connected": false, "score": 0}
	g.emit(s, "player_joined", M{"player": pv, "game": g.view()})
	g.emit(s, "lobby_updated", M{"game": g.view()})
	return g.ID, nil
}

func (s *Service) presence(gid, uid uint, online bool) {
	g, err := s.get(gid)
	if err != nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.Players[uid]
	if p == nil {
		return
	}
	p.Connected = online
	if !online {
		g.emit(s, "player_left", M{"playerId": sid(uid), "game": g.view()})
	}
	g.emit(s, "lobby_updated", M{"game": g.view()})
	if !online && g.Status == "playing" && g.Round != nil {
		s.maybeEndLocked(g, g.Round)
	}
}

func (s *Service) IsMember(gid, uid uint) bool {
	g, err := s.get(gid)
	if err != nil {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.Players[uid] != nil
}

func (s *Service) Start(gid, uid uint) error {
	g, err := s.get(gid)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Players[uid] == nil {
		return apperr.NotInGame
	}
	if g.HostID != uid {
		return apperr.Forbidden
	}
	switch g.Status {
	case "playing":
		return apperr.AlreadyStarted
	case "finished":
		return apperr.AlreadyFinished
	}
	if len(g.Players) < s.Cfg.MinPlayers {
		return apperr.NotEnough
	}
	if err := s.startRoundLocked(g, true); err != nil {
		return err
	}
	return nil
}

// playable treats NULL and empty audio_path the same (NULL <> ” is NULL in SQL, which silently excluded rows).
const playable = "COALESCE(audio_path, '') <> ''"

func (s *Service) pickSong(g *live) (models.Song, error) {
	var song models.Song
	if len(g.Used) > 0 {
		ids := make([]uint, 0, len(g.Used))
		for id := range g.Used {
			ids = append(ids, id)
		}
		err := s.DB.Order("random()").Where("id NOT IN ? AND "+playable, ids).First(&song).Error
		if err == nil {
			return song, nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return song, err
		}
		g.Used = map[uint]bool{} // not enough songs: allow repeats
	}
	err := s.DB.Order("random()").Where(playable).First(&song).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		var total, withAudio int64
		s.DB.Model(&models.Song{}).Count(&total)
		s.DB.Model(&models.Song{}).Where(playable).Count(&withAudio)
		log.Printf("NO_SONGS: songs table has %d rows, %d with an audio_path (rows without audio are never picked)", total, withAudio)
		return song, apperr.NoSongs
	}
	return song, err
}

// startRoundLocked: caller holds g.mu.
func (s *Service) startRoundLocked(g *live, first bool) error {
	song, err := s.pickSong(g)
	if err != nil {
		return err
	}
	tok, err := utils.RandToken("a_", 32)
	if err != nil {
		return err
	}
	dur := time.Duration(g.RoundSecs) * time.Second
	now := time.Now()
	r := models.Round{GameID: g.ID, RoundNumber: g.RoundNum + 1, SongID: song.ID, StartedAt: now, Status: "active"}
	if err := s.DB.Create(&r).Error; err != nil {
		return err
	}
	if first {
		if err := s.DB.Model(&models.Game{}).Where("id=?", g.ID).Updates(M{"status": "in_progress", "started_at": now}).Error; err != nil {
			return err
		}
	}
	if err := s.RDB.Set(context.Background(), "audio:"+tok, fmt.Sprintf("%d:%d", g.ID, r.ID), dur+15*time.Second).Err(); err != nil {
		return err
	}
	rs := &roundState{ID: r.ID, Num: r.RoundNumber, Answer: song, StartedAt: now, EndsAt: now.Add(dur), Token: tok, Guesses: map[uint]string{}, Solved: map[uint]bool{}, Wrong: map[uint]int{}, Pts: map[uint]int{}}
	rs.timer = time.AfterFunc(dur, func() { g.mu.Lock(); defer g.mu.Unlock(); s.endLocked(g, rs) })
	g.Used[song.ID] = true
	g.Round, g.RoundNum, g.Last, g.Status = rs, r.RoundNumber, nil, "playing"
	if first {
		g.emit(s, "game_started", M{"gameId": sid(g.ID), "totalRounds": g.Total})
	}
	g.emit(s, "round_started", g.roundView(rs))
	return nil
}

// endLocked ends a round exactly once (timer expiry, all-submitted, or late guess). Caller holds g.mu.
func (s *Service) endLocked(g *live, rs *roundState) {
	if rs.ending || g.Round != rs {
		return
	}
	rs.ending = true
	rs.timer.Stop()
	s.RDB.Del(context.Background(), "audio:"+rs.Token) // token dies with the round
	now := time.Now()
	if err := s.DB.Model(&models.Round{}).Where("id=?", rs.ID).Updates(M{"status": "ended", "ended_at": now}).Error; err != nil {
		log.Printf("end round: %v", err)
	}
	ps := []M{}
	for _, id := range g.Order {
		if !rs.Solved[id] { // solved players were saved when they guessed; record the rest now
			ms := int(now.Sub(rs.StartedAt).Milliseconds())
			if err := s.DB.Create(&models.Guess{RoundID: rs.ID, UserID: id, Guess: rs.Guesses[id], ResponseTimeMs: ms}).Error; err != nil {
				log.Printf("save guess: %v", err)
			}
		}
		ps = append(ps, M{"playerId": sid(id), "name": g.Players[id].Name, "guess": rs.Guesses[id], "correct": rs.Solved[id], "points": rs.Pts[id]})
	}
	a := rs.Answer
	res := M{"roundNumber": rs.Num, "players": ps, "scores": g.scores(),
		"song": M{"title": a.Title, "artist": a.Artist, "album": a.Album, "artworkUrl": a.ArtworkURL}}
	reveal := time.Duration(s.Cfg.RevealSeconds) * time.Second
	if rs.Num < g.Total {
		res["nextRoundAt"] = ts(now.Add(reveal))
	}
	g.Last = res
	g.emit(s, "round_ended", M{"roundId": sid(rs.ID), "roundNumber": rs.Num})
	g.emit(s, "round_result", res)
	g.emit(s, "leaderboard_updated", M{"scores": g.scores()})
	time.AfterFunc(reveal, func() { g.mu.Lock(); defer g.mu.Unlock(); s.advanceLocked(g, rs) })
}

func (s *Service) advanceLocked(g *live, prev *roundState) {
	if g.Round != prev || g.Status != "playing" {
		return
	}
	if g.RoundNum >= g.Total {
		s.finishLocked(g)
		return
	}
	if err := s.startRoundLocked(g, false); err != nil {
		log.Printf("next round: %v", err)
		s.finishLocked(g)
	}
}

func (s *Service) finishLocked(g *live) {
	out := []M{}
	rank, prev := 0, -1
	err := s.DB.Transaction(func(tx *gorm.DB) error {
		for i, p := range g.sorted() {
			if p.Score != prev {
				rank, prev = i+1, p.Score
			}
			if err := tx.Model(&models.GamePlayer{}).Where("game_id=? AND user_id=?", g.ID, p.ID).Update("final_rank", rank).Error; err != nil {
				return err
			}
			out = append(out, M{"playerId": sid(p.ID), "name": p.Name, "score": p.Score, "rank": rank})
		}
		return tx.Model(&models.Game{}).Where("id=?", g.ID).Updates(M{"status": "finished", "ended_at": time.Now()}).Error
	})
	if err != nil {
		log.Printf("finish game: %v", err)
	}
	g.Status = "finished"
	g.emit(s, "game_finished", M{"scores": out})
	time.AfterFunc(30*time.Minute, func() {
		s.mu.Lock()
		delete(s.games, g.ID)
		s.mu.Unlock()
		s.RDB.Del(context.Background(), "room:"+g.Code)
	})
}

// Guess lets a player try repeatedly until correct. A wrong guess costs one second of score
// and unlocks one more second of audio (enforced by the client); a correct one locks the player in.
func (s *Service) Guess(gid, uid, roundID uint, text string) (M, error) {
	g, err := s.get(gid)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	p := g.Players[uid]
	if p == nil {
		return nil, apperr.NotInGame
	}
	rs := g.Round
	if g.Status != "playing" || rs == nil || rs.ending || rs.ID != roundID {
		return nil, apperr.RoundExpired
	}
	now := time.Now() // server clock decides; client timing is never trusted
	if !now.Before(rs.EndsAt) {
		s.endLocked(g, rs)
		return nil, apperr.RoundExpired
	}
	if rs.Solved[uid] {
		return nil, apperr.Duplicate
	}
	text = strings.TrimSpace(text)
	if text == "" || len([]rune(text)) > 200 {
		return nil, apperr.BadRequest
	}
	rs.Guesses[uid] = text
	if !utils.Matches(text, rs.Answer.Title) {
		rs.Wrong[uid]++
		return M{"correct": false, "wrong": rs.Wrong[uid]}, nil
	}
	pts := utils.Points(rs.EndsAt.Sub(now), rs.EndsAt.Sub(rs.StartedAt), rs.Wrong[uid])
	if err := s.DB.Transaction(func(tx *gorm.DB) error {
		row := models.Guess{RoundID: rs.ID, UserID: uid, Guess: text, IsCorrect: true, Points: pts, ResponseTimeMs: int(now.Sub(rs.StartedAt).Milliseconds())}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		return tx.Model(&models.GamePlayer{}).Where("game_id=? AND user_id=?", g.ID, uid).UpdateColumn("score", gorm.Expr("score + ?", pts)).Error
	}); err != nil {
		return nil, err
	}
	rs.Solved[uid], rs.Pts[uid] = true, pts
	p.Score += pts
	// Only counts are broadcast; other players' guesses and points stay hidden until round_result.
	g.emit(s, "player_submitted", M{"playerId": sid(uid), "answeredCount": len(rs.Solved), "totalPlayers": len(g.Players)})
	s.maybeEndLocked(g, rs)
	return M{"correct": true, "points": pts, "wrong": rs.Wrong[uid]}, nil
}

// maybeEndLocked ends the round early once every connected player has solved it. Caller holds g.mu.
func (s *Service) maybeEndLocked(g *live, rs *roundState) {
	if len(rs.Solved) == 0 {
		return
	}
	for id, p := range g.Players {
		if p.Connected && !rs.Solved[id] {
			return
		}
	}
	s.endLocked(g, rs)
}

// Snapshot is what the client rehydrates from on refresh/reconnect. Contains no answer data.
func (s *Service) Snapshot(gid, uid uint) (M, error) {
	g, err := s.get(gid)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Players[uid] == nil {
		return nil, apperr.NotInGame
	}
	out := M{"game": g.view(), "scores": g.scores(), "serverTime": ts(time.Now()), "answered": []string{}, "ended": false}
	if rs := g.Round; rs != nil {
		ans := []string{}
		for id := range rs.Solved {
			ans = append(ans, sid(id))
		}
		out["answered"] = ans
		out["wrong"] = rs.Wrong[uid]
		if rs.Solved[uid] {
			out["myGuess"] = rs.Guesses[uid]
		}
		if rs.ending {
			out["ended"] = true
			if g.Last != nil {
				out["result"] = g.Last
			}
		} else {
			out["round"] = g.roundView(rs)
		}
	}
	return out, nil
}

func (s *Service) Standings(gid, uid uint) (M, error) {
	g, err := s.get(gid)
	if err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Players[uid] == nil {
		return nil, apperr.NotInGame
	}
	if g.Status != "finished" {
		return nil, apperr.NotFinished
	}
	return M{"scores": g.scores()}, nil
}

// AudioSource validates an opaque token and returns the private storage path (never sent to the client).
func (s *Service) AudioSource(ctx context.Context, gid, uid uint, token string) (string, error) {
	v, err := s.RDB.Get(ctx, "audio:"+token).Result()
	if err != nil {
		return "", apperr.InvalidToken
	}
	var tg, tr uint
	if _, err := fmt.Sscanf(v, "%d:%d", &tg, &tr); err != nil || tg != gid {
		return "", apperr.InvalidToken // token belongs to another game
	}
	g, err := s.get(gid)
	if err != nil {
		return "", err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Players[uid] == nil {
		return "", apperr.NotInGame
	}
	rs := g.Round
	if rs == nil || rs.ending || rs.ID != tr || rs.Token != token || time.Now().After(rs.EndsAt) {
		return "", apperr.AudioUnavailable
	}
	return rs.Answer.AudioPath, nil
}

// SetRoundSeconds lets the host choose the round length while the game is still in the lobby.
func (s *Service) SetRoundSeconds(gid, uid uint, secs int) error {
	if secs < 5 || secs > 120 {
		return apperr.BadRequest
	}
	g, err := s.get(gid)
	if err != nil {
		return err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.Players[uid] == nil {
		return apperr.NotInGame
	}
	if g.HostID != uid {
		return apperr.Forbidden
	}
	if g.Status != "lobby" {
		return apperr.AlreadyStarted
	}
	g.RoundSecs = secs
	g.emit(s, "lobby_updated", M{"game": g.view()})
	return nil
}

type SongSuggestion struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

// SearchSongs powers the guess autocomplete: title/artist pairs only (never IDs) from the playable catalog,
// so it reveals nothing about which song is the current answer.
func (s *Service) SearchSongs(q string) ([]SongSuggestion, error) {
	q = strings.TrimSpace(q)
	out := []SongSuggestion{}
	if len([]rune(q)) < 2 {
		return out, nil
	}
	esc := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	start, word := esc+"%", "% "+esc+"%"
	err := s.DB.Model(&models.Song{}).
		Select("DISTINCT title, artist").
		Where(playable).
		Where("title ILIKE ? OR title ILIKE ? OR artist ILIKE ? OR artist ILIKE ?", start, word, start, word).
		Order("title").
		Limit(8).
		Scan(&out).Error
	return out, err
}
