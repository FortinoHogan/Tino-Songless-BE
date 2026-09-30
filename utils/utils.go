package utils

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/golang-jwt/jwt/v5"
)

// RandToken returns prefix + n cryptographically random bytes (base64url). Reveals nothing about any song.
func RandToken(prefix string, n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b), nil
}

const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 32 chars, no lookalikes

func RoomCode() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}

func SignJWT(secret string, uid uint) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": uid, "exp": time.Now().Add(24 * time.Hour).Unix()}).SignedString([]byte(secret))
}

func ParseJWT(secret, tok string) (uint, error) {
	t, err := jwt.Parse(tok, func(*jwt.Token) (any, error) { return []byte(secret), nil }, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !t.Valid {
		return 0, errors.New("invalid token")
	}
	m, ok := t.Claims.(jwt.MapClaims)
	f, ok2 := m["sub"].(float64)
	if !ok || !ok2 {
		return 0, errors.New("invalid claims")
	}
	return uint(f), nil
}

// Points: 10000 at the start of the round, falling linearly to 0 as time runs out.
// Every wrong guess costs one more second's worth of score.
func Points(remaining, total time.Duration, wrong int) int {
	if total <= 0 {
		return 0
	}
	left := remaining.Seconds() - float64(wrong)
	return max(0, min(10000, int(10000*left/total.Seconds())))
}

var paren = regexp.MustCompile(`[\(\[].*?[\)\]]`)

func Normalize(s string) string {
	s = strings.ToLower(paren.ReplaceAllString(s, ""))
	var b strings.Builder
	sp := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			sp = false
		} else if !sp && b.Len() > 0 {
			b.WriteByte(' ')
			sp = true
		}
	}
	return strings.TrimSpace(b.String())
}

// Matches: normalized equality, or a small typo tolerance on longer titles.
func Matches(guess, answer string) bool {
	g, a := Normalize(guess), Normalize(answer)
	if g == "" || a == "" {
		return false
	}
	return g == a || (len(a) >= 8 && lev(g, a) <= len(a)/8)
}

func lev(a, b string) int {
	x, y := []rune(a), []rune(b)
	prev := make([]int, len(y)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(x); i++ {
		cur := make([]int, len(y)+1)
		cur[0] = i
		for j := 1; j <= len(y); j++ {
			c := 1
			if x[i-1] == y[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev = cur
	}
	return prev[len(y)]
}
