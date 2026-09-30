package importer

import (
	"strings"

	"songbattle/models"
)

// ToSong maps a provider track to our local model; ok=false if required fields are missing.
func ToSong(provider string, t Track) (models.Song, bool) {
	s := models.Song{Provider: provider, ProviderSongID: strings.TrimSpace(t.ProviderID), Title: strings.TrimSpace(t.Title),
		Artist: strings.TrimSpace(t.Artist), Album: strings.TrimSpace(t.Album), Genre: strings.TrimSpace(t.Genre), ArtworkURL: t.ArtworkURL}
	return s, s.ProviderSongID != "" && s.Title != "" && s.Artist != ""
}
