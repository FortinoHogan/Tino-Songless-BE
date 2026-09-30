package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait = 10 * time.Second
	pongWait  = 60 * time.Second
	pingEvery = 25 * time.Second
)

type Client struct {
	hub            *Hub
	conn           *websocket.Conn
	send           chan []byte
	gameID, userID uint
	once           sync.Once
}

func NewClient(h *Hub, conn *websocket.Conn, gameID, userID uint) *Client {
	return &Client{hub: h, conn: conn, send: make(chan []byte, 64), gameID: gameID, userID: userID}
}

// Hub tracks connections per game. OnPresence fires when a user's first connection opens / last one closes.
type Hub struct {
	mu         sync.RWMutex
	rooms      map[uint]map[*Client]struct{}
	OnPresence func(gameID, userID uint, online bool)
}

func NewHub() *Hub { return &Hub{rooms: map[uint]map[*Client]struct{}{}} }

func (h *Hub) present(g, u uint) bool { // caller holds lock
	for c := range h.rooms[g] {
		if c.userID == u {
			return true
		}
	}
	return false
}

func (h *Hub) Add(c *Client) {
	h.mu.Lock()
	was := h.present(c.gameID, c.userID)
	if h.rooms[c.gameID] == nil {
		h.rooms[c.gameID] = map[*Client]struct{}{}
	}
	h.rooms[c.gameID][c] = struct{}{}
	h.mu.Unlock()
	if !was && h.OnPresence != nil {
		go h.OnPresence(c.gameID, c.userID, true)
	}
}

func (h *Hub) remove(c *Client) {
	c.once.Do(func() {
		h.mu.Lock()
		delete(h.rooms[c.gameID], c)
		if len(h.rooms[c.gameID]) == 0 {
			delete(h.rooms, c.gameID)
		}
		still := h.present(c.gameID, c.userID)
		h.mu.Unlock()
		close(c.send)
		_ = c.conn.Close()
		if !still && h.OnPresence != nil {
			go h.OnPresence(c.gameID, c.userID, false)
		}
	})
}

// Broadcast never blocks: clients with a full buffer are dropped and can resync via REST.
func (h *Hub) Broadcast(gameID uint, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.rooms[gameID] {
		select {
		case c.send <- msg:
		default:
			go h.remove(c)
		}
	}
}

func (c *Client) ReadPump() {
	defer c.hub.remove(c)
	c.conn.SetReadLimit(1024)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error { return c.conn.SetReadDeadline(time.Now().Add(pongWait)) })
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil { // clients only listen; reads keep pongs flowing
			return
		}
	}
}

func (c *Client) WritePump() {
	t := time.NewTicker(pingEvery)
	defer t.Stop()
	for {
		select {
		case m, ok := <-c.send:
			if !ok {
				return
			}
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if c.conn.WriteMessage(websocket.TextMessage, m) != nil {
				c.hub.remove(c)
				return
			}
		case <-t.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if c.conn.WriteMessage(websocket.PingMessage, nil) != nil {
				c.hub.remove(c)
				return
			}
		}
	}
}
