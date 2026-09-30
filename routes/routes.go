package routes

import (
	"songbattle/config"
	"songbattle/handlers"

	"github.com/gin-gonic/gin"
)

func cors(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Vary", "Origin")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range")
		h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	}
}

func Register(r *gin.Engine, h *handlers.H, cfg config.Config) {
	r.Use(cors(cfg.CORSOrigin))
	r.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok"}) })
	r.GET("/ws", h.WS)
	r.GET("/ws/games/:gameId", h.WS)
	api := r.Group("/api")
	api.POST("/games", h.Create)
	api.POST("/games/join", h.Join)
	api.GET("/songs/search", handlers.Auth(cfg.JWTSecret), h.SongSearch)
	g := api.Group("/games/:gameId", handlers.Auth(cfg.JWTSecret))
	g.GET("", h.Snapshot)
	g.POST("/start", h.Start)
	g.POST("/guess", h.Guess)
	g.POST("/settings", h.Settings)
	g.GET("/result", h.Result)
	g.GET("/audio/:audioToken", h.Audio)
}
