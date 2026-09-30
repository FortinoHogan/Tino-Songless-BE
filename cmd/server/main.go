package main

import (
	"log"
	"net/http"
	"time"

	"songbattle/config"
	"songbattle/database"
	"songbattle/handlers"
	"songbattle/routes"
	"songbattle/services"
	"songbattle/storage"
	"songbattle/ws"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.Load()
	db, err := database.OpenPostgres(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("postgres: %v", err)
	}
	rdb, err := database.OpenRedis(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis: %v", err)
	}
	hub := ws.NewHub()
	svc := services.NewService(db, rdb, hub, cfg)
	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery())
	routes.Register(r, &handlers.H{S: svc, Store: storage.New(cfg.SupabaseURL, cfg.AudioBucket, cfg.ServiceKey), Hub: hub, Cfg: cfg}, cfg)
	srv := &http.Server{Addr: ":" + cfg.Port, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	log.Printf("listening on :%s", cfg.Port)
	log.Fatal(srv.ListenAndServe())
}
