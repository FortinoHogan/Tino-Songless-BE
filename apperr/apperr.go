package apperr

type APIError struct {
	Status        int
	Code, Message string
}

func (e *APIError) Error() string { return e.Message }
func e(s int, c, m string) *APIError { return &APIError{s, c, m} }

var (
	BadRequest       = e(400, "BAD_REQUEST", "Invalid request")
	Unauthorized     = e(401, "UNAUTHORIZED", "Authentication required")
	Forbidden        = e(403, "FORBIDDEN", "You are not allowed to do that")
	NotInGame        = e(403, "PLAYER_NOT_IN_GAME", "You are not in this game")
	InvalidToken     = e(403, "INVALID_TOKEN", "Invalid or expired token")
	GameNotFound     = e(404, "GAME_NOT_FOUND", "Game not found")
	InvalidRoom      = e(404, "INVALID_ROOM", "Invalid room code")
	AudioUnavailable = e(404, "AUDIO_UNAVAILABLE", "Audio unavailable")
	AlreadyStarted   = e(409, "GAME_ALREADY_STARTED", "Game already started")
	AlreadyFinished  = e(409, "GAME_ALREADY_FINISHED", "Game already finished")
	NotFinished      = e(409, "GAME_NOT_FINISHED", "Game has not finished")
	RoundExpired     = e(409, "ROUND_EXPIRED", "Round is not active")
	Duplicate        = e(409, "DUPLICATE_GUESS", "You already submitted a guess")
	RoomFull         = e(409, "ROOM_FULL", "Room is full")
	NameTaken        = e(409, "NAME_TAKEN", "That name is taken in this room")
	NotEnough        = e(409, "NOT_ENOUGH_PLAYERS", "Not enough players to start")
	NoSongs          = e(503, "NO_SONGS", "No songs available")
)
