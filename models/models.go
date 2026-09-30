package models

import "time"

type User struct {
	ID                               uint `gorm:"primaryKey"`
	Username, DisplayName, AvatarURL string
	CreatedAt                        time.Time
}
type Group struct {
	ID               uint `gorm:"primaryKey"`
	Name, InviteCode string
	CreatedAt        time.Time
}
type GroupMember struct {
	GroupID  uint `gorm:"primaryKey"`
	UserID   uint `gorm:"primaryKey"`
	JoinedAt time.Time
}
type Song struct {
	ID                                                                           uint `gorm:"primaryKey"`
	Provider, ProviderSongID, Title, Artist, Album, Genre, ArtworkURL, AudioPath string
	CreatedAt                                                                    time.Time
}
type Game struct {
	ID                 uint `gorm:"primaryKey"`
	GroupID            *uint
	Status, GameMode   string
	StartedAt, EndedAt *time.Time
	CreatedAt          time.Time
}
type GamePlayer struct {
	GameID    uint `gorm:"primaryKey"`
	UserID    uint `gorm:"primaryKey"`
	Score     int
	FinalRank *int
}
type Round struct {
	ID          uint `gorm:"primaryKey"`
	GameID      uint
	RoundNumber int
	SongID      uint
	StartedAt   time.Time
	EndedAt     *time.Time
	Status      string
}
type Guess struct {
	ID             uint `gorm:"primaryKey"`
	RoundID        uint
	UserID         uint
	Guess          string
	IsCorrect      bool
	Points         int
	ResponseTimeMs int
	CreatedAt      time.Time
}

func (GamePlayer) TableName() string {
	return "GamePlayers"
}

func (Game) TableName() string {
	return "Games"
}

func (GroupMember) TableName() string {
	return "GroupMembers"
}

func (Group) TableName() string {
	return "Groups"
}

func (Guess) TableName() string {
	return "Guesses"
}

func (Round) TableName() string {
	return "Rounds"
}

func (Song) TableName() string {
	return "Songs"
}

func (User) TableName() string {
	return "Users"
}
