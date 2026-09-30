package handlers

import (
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"songbattle/apperr"
	"songbattle/config"
	"songbattle/services"
	"songbattle/storage"
	"songbattle/utils"
	"songbattle/ws"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type H struct {
	S     *services.Service
	Store *storage.Client
	Hub   *ws.Hub
	Cfg   config.Config
}

func fail(c *gin.Context, err error) {
	var ae *apperr.APIError
	if errors.As(err, &ae) {
		c.AbortWithStatusJSON(ae.Status, gin.H{"error": gin.H{"code": ae.Code, "message": ae.Message}})
		return
	}
	log.Printf("internal error: %v", err)
	c.AbortWithStatusJSON(500, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": "Internal server error"}})
}

func bearer(c *gin.Context) string {
	return strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
}

func Auth(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
		u, err := utils.ParseJWT(secret, bearer(c))
		if err != nil {
			fail(c, apperr.Unauthorized)
			return
		}
		c.Set("uid", u)
		c.Next()
	}
}

func uid(c *gin.Context) uint { return c.MustGet("uid").(uint) }

func gid(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("gameId"), 10, 64)
	if err != nil {
		fail(c, apperr.GameNotFound)
		return 0, false
	}
	return uint(n), true
}

// session resolves the caller: a valid bearer token, or (no header) a new guest user from displayName.
func (h *H) session(c *gin.Context, name string) (uint, string, bool) {
	if c.GetHeader("Authorization") != "" {
		tok := bearer(c)
		u, err := utils.ParseJWT(h.Cfg.JWTSecret, tok)
		if err != nil {
			fail(c, apperr.Unauthorized)
			return 0, "", false
		}
		return u, tok, true
	}
	u, err := h.S.GuestUser(name)
	if err == nil {
		var tok string
		if tok, err = utils.SignJWT(h.Cfg.JWTSecret, u); err == nil {
			return u, tok, true
		}
	}
	fail(c, err)
	return 0, "", false
}

func (h *H) Create(c *gin.Context) {
	var in struct {
		GroupID     *uint  `json:"groupId"`
		GameMode    string `json:"gameMode"`
		TotalRounds int    `json:"totalRounds"`
		DisplayName string `json:"displayName"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, apperr.BadRequest)
		return
	}
	u, tok, ok := h.session(c, in.DisplayName)
	if !ok {
		return
	}
	id, code, err := h.S.Create(c.Request.Context(), u, in.GroupID, in.GameMode, in.TotalRounds)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(201, gin.H{"gameId": strconv.FormatUint(uint64(id), 10), "roomCode": code, "code": code, "playerId": strconv.FormatUint(uint64(u), 10), "token": tok})
}

func (h *H) Join(c *gin.Context) {
	var in struct {
		RoomCode    string `json:"roomCode"`
		Code        string `json:"code"`
		DisplayName string `json:"displayName"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, apperr.BadRequest)
		return
	}
	if in.RoomCode == "" {
		in.RoomCode = in.Code
	}
	u, tok, ok := h.session(c, in.DisplayName) // body userId is ignored: identity comes from the token
	if !ok {
		return
	}
	id, err := h.S.Join(c.Request.Context(), in.RoomCode, u)
	if err != nil {
		fail(c, err)
		return
	}
	snap, err := h.S.Snapshot(id, u)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, gin.H{"gameId": strconv.FormatUint(uint64(id), 10), "roomCode": strings.ToUpper(in.RoomCode), "playerId": strconv.FormatUint(uint64(u), 10), "token": tok, "game": snap["game"]})
}

func (h *H) Snapshot(c *gin.Context) {
	g, ok := gid(c)
	if !ok {
		return
	}
	snap, err := h.S.Snapshot(g, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, snap)
}

func (h *H) Start(c *gin.Context) {
	g, ok := gid(c)
	if !ok {
		return
	}
	if err := h.S.Start(g, uid(c)); err != nil {
		fail(c, err)
		return
	}
	c.Status(204)
}

func (h *H) Guess(c *gin.Context) {
	g, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		RoundID string `json:"roundId"`
		Guess   string `json:"guess"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, apperr.BadRequest)
		return
	}
	rid, err := strconv.ParseUint(in.RoundID, 10, 64)
	if err != nil {
		fail(c, apperr.RoundExpired)
		return
	}
	res, err := h.S.Guess(g, uid(c), uint(rid), in.Guess)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, res) // {correct, points?, wrong}: only the submitter learns whether they were right
}

func (h *H) Result(c *gin.Context) {
	g, ok := gid(c)
	if !ok {
		return
	}
	res, err := h.S.Standings(g, uid(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, res)
}

// Audio proxies the file from the private Supabase bucket so the storage path never reaches the browser.
func (h *H) Audio(c *gin.Context) {
	g, ok := gid(c)
	if !ok {
		return
	}
	path, err := h.S.AudioSource(c.Request.Context(), g, uid(c), c.Param("audioToken"))
	if err != nil {
		fail(c, err)
		return
	}
	resp, err := h.Store.Open(c.Request.Context(), path, c.GetHeader("Range"))
	if err != nil {
		fail(c, apperr.AudioUnavailable)
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			c.Header(k, v)
		}
	}
	c.Header("Cache-Control", "no-store")
	c.Status(resp.StatusCode)
	_, _ = io.Copy(c.Writer, resp.Body)
}

func (h *H) WS(c *gin.Context) {
	idStr := c.Param("gameId")
	if idStr == "" {
		idStr = c.Query("gameId")
	}
	n, err := strconv.ParseUint(idStr, 10, 64)
	if err != nil {
		fail(c, apperr.GameNotFound)
		return
	}
	u, err := utils.ParseJWT(h.Cfg.JWTSecret, c.Query("token")) // browsers can't set WS headers
	if err != nil {
		fail(c, apperr.Unauthorized)
		return
	}
	if !h.S.IsMember(uint(n), u) {
		fail(c, apperr.NotInGame)
		return
	}
	up := websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024, CheckOrigin: func(r *http.Request) bool {
		o := r.Header.Get("Origin")
		return o == "" || h.Cfg.CORSOrigin == "*" || o == h.Cfg.CORSOrigin
	}}
	conn, err := up.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}
	cl := ws.NewClient(h.Hub, conn, uint(n), u)
	h.Hub.Add(cl)
	go cl.WritePump()
	cl.ReadPump()
}

func (h *H) Settings(c *gin.Context) {
	g, ok := gid(c)
	if !ok {
		return
	}
	var in struct {
		RoundSeconds int `json:"roundSeconds"`
	}
	if c.ShouldBindJSON(&in) != nil {
		fail(c, apperr.BadRequest)
		return
	}
	if err := h.S.SetRoundSeconds(g, uid(c), in.RoundSeconds); err != nil {
		fail(c, err)
		return
	}
	c.Status(204)
}

func (h *H) SongSearch(c *gin.Context) {
	res, err := h.S.SearchSongs(c.Query("q"))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(200, res)
}
