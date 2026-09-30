// Package app is the pet server: robots connect over the Embody Mode protocol
// (github.com/mj41/stackchan-server/wire), each robot has one pet, kids play
// on the robot and on a picture page, parents change settings behind a PIN.
//
// Pairing works like stackchan-server: the robot shows <public-url>/pair?code=...
// as a QR code; the browser that opens it may see and play with that pet.
package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
	"github.com/mj41/stackchan-server/wire"
)

// Config configures an App.
type Config struct {
	RobotToken string         // bearer token robots must present
	PublicURL  string         // base URL browsers use, e.g. http://192.168.1.10:8770
	PairTTL    time.Duration  // lifetime of a pairing code
	StateFile  string         // JSON file with pets and pairings; "" keeps them in memory only
	UIDir      string         // development: serve the pages from this directory (e.g. internal/app/ui)
	Location   *time.Location // the family's time zone for the schedule; default time.Local
	Log        *slog.Logger
	Now        func() time.Time // tests; default time.Now
	DebugDir   string           // where screen snapshots from the robot are saved (debug.go); "" = not saved
}

// App holds all state. One mutex guards everything; robot sockets only queue messages.
type App struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	robots   map[string]*robot
	codes    map[string]pairCode
	sessions map[string]*session
	subs     map[*subscriber]struct{}
	dirty    bool   // state changed since the last save
	timing   timing // the game's pace (game.go)
	saveMu   sync.Mutex
}

type pairCode struct {
	robotID string
	expires time.Time
}

// session is one browser. Robots are its paired robots, the most recent first.
type session struct {
	Robots []string `json:"robots"`

	parentUntil time.Time // the parent page is unlocked until then
	pinFails    int
	pinFailAt   time.Time
}

// robot is one robot and its pet; it stays after the robot disconnects.
type robot struct {
	id          string
	pet         *pet.Pet
	pinHash     string               // "salt:sha256hex" of the parent PIN, "" = not set yet
	unknownTags map[string]time.Time // NFC tags without a chosen food: uid -> last seen

	conn     *robotConn // nil while offline
	lastSeen time.Time

	// What the robot shows (not saved): the engine compares and reacts on changes.
	shownMood  pet.Mood
	phase      pet.Phase
	lastNag    time.Time
	lastHello  time.Time
	bedWarned  string            // the date of the last "bedtime soon" warning
	lastAsleep time.Time         // last sleepy answer at night, to not repeat it on every touch
	pictureOn  bool              // a picture covers the face
	screenOff  bool              // the pet turned the robot's screen off for the night
	busyUntil  time.Time         // a reaction shows until then; the mood waits
	gen        int               // bumped by each reaction; delayed steps of an older one are dropped
	game       *game             // a game of catch in progress (game.go)
	uploading  map[string]uint32 // pet files being uploaded -> their CRC-32 (assets.go)
	commands   []string          // what this robot's firmware accepts
	files      map[string]bool   // pet files on the robot, ready to show as sprites
	spriteIDs  map[string]bool   // the pet's sprites on screen (not the face): cleared with the mood
	faceShown  string            // the drawn face shown as the bottom sprite ("" = the robot's own face)
	faceHidden bool              // the drawn face steps aside for speech or a full-screen picture
	lastCard   time.Time         // the last food card: head touches around it are not cuddles
	cuddleGen  int               // bumped by a card: a waiting head touch is dropped
}

func New(cfg Config) *App {
	if cfg.PairTTL <= 0 {
		cfg.PairTTL = 5 * time.Minute
	}
	if cfg.Location == nil {
		cfg.Location = time.Local
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	a := &App{
		cfg:      cfg,
		log:      cfg.Log,
		robots:   map[string]*robot{},
		codes:    map[string]pairCode{},
		sessions: map[string]*session{},
		subs:     map[*subscriber]struct{}{},
		timing:   defaultTiming,
	}
	if cfg.StateFile != "" {
		if err := a.load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
			a.log.Warn("state not loaded, starting empty", "file", cfg.StateFile, "err", err)
		}
	}
	return a
}

// now is the current time in the family's time zone.
func (a *App) now() time.Time { return a.cfg.Now().In(a.cfg.Location) }

// Handler returns the HTTP routes.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+wire.ConnectPath, a.handleRobotConnect)
	mux.HandleFunc("GET /{$}", a.page("index.html"))
	mux.HandleFunc("GET /parent", a.page("parent.html"))
	mux.HandleFunc("GET /pair", a.handlePair)
	mux.HandleFunc("GET /api/state", a.handleState)
	mux.HandleFunc("GET /api/events", a.handleEvents)
	mux.HandleFunc("POST /api/action", a.handleAction)
	mux.HandleFunc("GET /api/parent", a.handleParent)
	mux.HandleFunc("POST /api/parent/unlock", a.handleUnlock)
	mux.HandleFunc("POST /api/parent/lock", a.handleLock)
	mux.HandleFunc("POST /api/parent/settings", a.handleSettings)
	mux.HandleFunc("POST /api/parent/pin", a.handlePIN)
	mux.HandleFunc("POST /api/parent/reset", a.handleReset)
	mux.HandleFunc("POST /api/parent/try", a.handleTry)
	mux.HandleFunc("POST /api/parent/stats", a.handleStats)
	mux.HandleFunc("POST /api/debug/{id}/run", a.handleDebugRun)
	mux.Handle("GET /emoji/", http.StripPrefix("/emoji/", a.uiFiles("emoji")))
	mux.Handle("GET /chan/", http.StripPrefix("/chan/", a.uiFiles("chan")))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	return mux
}

func (a *App) tokenOK(token string) bool {
	return a.cfg.RobotToken != "" && subtle.ConstantTimeCompare([]byte(token), []byte(a.cfg.RobotToken)) == 1
}

// robotFor returns the robot, creating it and its pet on first sight. a.mu held.
func (a *App) robotFor(id string) *robot {
	r := a.robots[id]
	if r == nil {
		r = &robot{id: id, pet: pet.New(a.now(), pet.DefaultSettings()), unknownTags: map[string]time.Time{}}
		a.robots[id] = r
		a.dirty = true
	}
	return r
}

/* --------------------------------- pairing -------------------------------- */

// Unambiguous characters only (no 0/O, 1/I), easy to read off a screen.
const codeAlphabet = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"

func newCode() string {
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = codeAlphabet[int(b[i])%len(codeAlphabet)]
	}
	return string(b)
}

// issueCode replaces the robot's pairing code. a.mu held.
func (a *App) issueCode(robotID string) wire.PairCodeBody {
	code := newCode()
	for c, pc := range a.codes {
		if pc.robotID == robotID {
			delete(a.codes, c)
		}
	}
	a.codes[code] = pairCode{robotID: robotID, expires: a.cfg.Now().Add(a.cfg.PairTTL)}
	return wire.PairCodeBody{
		Code:       code,
		URL:        a.cfg.PublicURL + "/pair?code=" + code,
		ExpiresInS: int(a.cfg.PairTTL / time.Second),
	}
}

// redeem pairs the session with the code's robot (first in its list). a.mu held.
func (a *App) redeem(sid, code string) (*robot, bool) {
	pc, ok := a.codes[code]
	delete(a.codes, code)
	if !ok || a.cfg.Now().After(pc.expires) {
		return nil, false
	}
	s := a.sessionFor(sid)
	s.Robots = append([]string{pc.robotID}, slices.DeleteFunc(s.Robots, func(id string) bool { return id == pc.robotID })...)
	a.dirty = true
	return a.robotFor(pc.robotID), true
}

// viewers is how many browsers are paired with the robot. a.mu held.
func (a *App) viewers(robotID string) int {
	n := 0
	for _, s := range a.sessions {
		if slices.Contains(s.Robots, robotID) {
			n++
		}
	}
	return n
}

/* -------------------------------- sessions -------------------------------- */

const sessionCookie = "stackchan_pet_session"

// sessionID returns the browser's session id, setting a new cookie if needed.
func (a *App) sessionID(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil && validSessionID(c.Value) {
		return c.Value
	}
	b := make([]byte, 32)
	rand.Read(b)
	id := hex.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		MaxAge:   int((365 * 24 * time.Hour) / time.Second),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil || strings.HasPrefix(a.cfg.PublicURL, "https://"),
	})
	return id
}

func validSessionID(v string) bool {
	if len(v) != 64 {
		return false
	}
	_, err := hex.DecodeString(v)
	return err == nil
}

// sessionFor returns the session, creating it. a.mu held.
func (a *App) sessionFor(sid string) *session {
	s := a.sessions[sid]
	if s == nil {
		s = &session{}
		a.sessions[sid] = s
	}
	return s
}

// pairedRobot is the session's robot: ?robot= if paired, else the most recent. a.mu held.
func (a *App) pairedRobot(sid, want string) *robot {
	s := a.sessions[sid]
	if s == nil {
		return nil
	}
	for _, id := range s.Robots {
		if want == "" || id == want {
			return a.robots[id]
		}
	}
	return nil
}

/* ---------------------------------- state --------------------------------- */

// stateFile is what is saved: pets and pairings, not live connections.
type stateFile struct {
	Robots   map[string]savedRobot `json:"robots"`
	Sessions map[string][]string   `json:"sessions"`
}

type savedRobot struct {
	Pet         *pet.Pet             `json:"pet"`
	PINHash     string               `json:"pin_hash,omitempty"`
	UnknownTags map[string]time.Time `json:"unknown_tags,omitempty"`
}

func (a *App) load() error {
	b, err := os.ReadFile(a.cfg.StateFile)
	if err != nil {
		return err
	}
	var st stateFile
	if err := json.Unmarshal(b, &st); err != nil {
		return err
	}
	for id, sr := range st.Robots {
		if sr.Pet == nil {
			continue
		}
		sr.Pet.Settings.Normalize()
		if sr.UnknownTags == nil {
			sr.UnknownTags = map[string]time.Time{}
		}
		a.robots[id] = &robot{id: id, pet: sr.Pet, pinHash: sr.PINHash, unknownTags: sr.UnknownTags}
	}
	for sid, ids := range st.Sessions {
		if validSessionID(sid) {
			a.sessions[sid] = &session{Robots: ids}
		}
	}
	a.log.Info("state loaded", "file", a.cfg.StateFile, "pets", len(a.robots), "sessions", len(a.sessions))
	return nil
}

// Save writes the state file if anything changed (atomically: temp file, rename).
func (a *App) Save() error {
	if a.cfg.StateFile == "" {
		return nil
	}
	a.saveMu.Lock()
	defer a.saveMu.Unlock()
	a.mu.Lock()
	if !a.dirty {
		a.mu.Unlock()
		return nil
	}
	st := stateFile{Robots: map[string]savedRobot{}, Sessions: map[string][]string{}}
	for id, r := range a.robots {
		r.pet.Advance(a.now())
		st.Robots[id] = savedRobot{Pet: r.pet, PINHash: r.pinHash, UnknownTags: r.unknownTags}
	}
	for sid, s := range a.sessions {
		if len(s.Robots) > 0 {
			st.Sessions[sid] = s.Robots
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	a.dirty = false
	a.mu.Unlock()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.cfg.StateFile), 0o700); err != nil {
		return err
	}
	tmp := a.cfg.StateFile + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, a.cfg.StateFile)
}

// Run drives the pets (every 15 s) and saves the state (every minute) until ctx ends.
func (a *App) Run(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	save := time.NewTicker(time.Minute)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			a.Tick()
		case <-save.C:
			if err := a.Save(); err != nil {
				a.log.Warn("state not saved", "err", err)
			}
		}
	}
}
