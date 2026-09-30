package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
	"github.com/mj41/stackchan-server/wire"
)

//go:embed ui/index.html ui/parent.html ui/emoji ui/chan
var uiFS embed.FS

// page serves one of the UI pages (from UIDir during development).
func (a *App) page(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.sessionID(w, r)
		b, err := uiFS.ReadFile("ui/" + name)
		if a.cfg.UIDir != "" {
			b, err = os.ReadFile(filepath.Join(a.cfg.UIDir, name))
		}
		if err != nil {
			http.Error(w, "page missing", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(b)
	}
}

// uiFiles serves a picture folder: emoji (Fluent Emoji Flat, MIT; see ui/emoji/LICENSE)
// or chan (the tiny Stack-chan faces; see ui/chan/NOTICE).
func (a *App) uiFiles(dir string) http.Handler {
	if a.cfg.UIDir != "" {
		return http.FileServer(http.Dir(filepath.Join(a.cfg.UIDir, dir)))
	}
	sub, _ := fs.Sub(uiFS, "ui/"+dir)
	return http.FileServerFS(sub)
}

/* ---------------------------------- views --------------------------------- */

// stateView is what the kid's page shows.
type stateView struct {
	Robot    string     `json:"robot"`
	Online   bool       `json:"online"`
	Name     string     `json:"name"`
	Lang     string     `json:"lang"`
	Mood     pet.Mood   `json:"mood"`
	Phase    pet.Phase  `json:"phase"`
	Stats    pet.Stats  `json:"stats"`
	AgeDays  int        `json:"age_days"`
	PlayLeft float64    `json:"play_left"` // minutes, -1 = no limit
	Wake     string     `json:"wake"`      // the next wake-up
	Bed      string     `json:"bed"`       // today's bedtime
	NapUntil *time.Time `json:"nap_until,omitempty"`
	Foods    []string   `json:"foods"`
	Game     *gameView  `json:"game,omitempty"` // a game of catch on the robot
}

// view builds the kid's view. a.mu held.
func (a *App) view(r *robot) stateView {
	now := a.now()
	p := r.pet
	p.Advance(now)
	day := p.Settings.Day(now)
	v := stateView{
		Robot: r.id, Online: r.conn != nil, Name: p.Settings.Name, Lang: p.Settings.Lang,
		Mood: p.Mood(now), Phase: p.Phase(now), Stats: p.Stats, AgeDays: p.AgeDays(now),
		Game: r.game.view(), PlayLeft: p.PlayLeft(now), Wake: p.Settings.NextWake(now), Bed: day.Bed, Foods: pet.FoodOrder,
	}
	if p.Napping(now) {
		t := p.NapUntil
		v.NapUntil = &t
	}
	return v
}

/* ----------------------------------- SSE ---------------------------------- */

type subscriber struct {
	sid    string
	events chan sseEvent
}

type sseEvent struct {
	name string
	data []byte
}

// publish sends ev to the browsers paired with the robot. a.mu held.
func (a *App) publish(robotID string, ev sseEvent) {
	for sub := range a.subs {
		if s := a.sessions[sub.sid]; s == nil || !slices.Contains(s.Robots, robotID) {
			continue
		}
		select {
		case sub.events <- ev:
		default: // a slow browser gets the next state
		}
	}
}

func (a *App) publishState(r *robot) {
	a.publish(r.id, sseEvent{"state", mustJSON(a.view(r))})
}

func (a *App) publishReaction(r *robot, re pet.Reaction) {
	a.publish(r.id, sseEvent{"reaction", mustJSON(map[string]any{"robot": r.id, "kind": re.Kind, "food": re.Food, "hits": re.Hits, "changed": re.Changed})})
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// handleEvents streams "state" (stateView) and "reaction" events for the session's robot.
func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	sub := &subscriber{sid: sid, events: make(chan sseEvent, 32)}
	a.mu.Lock()
	a.subs[sub] = struct{}{}
	var first []byte
	if rb := a.pairedRobot(sid, r.URL.Query().Get("robot")); rb != nil {
		first = mustJSON(a.view(rb))
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.subs, sub)
		a.mu.Unlock()
	}()

	want := r.URL.Query().Get("robot")
	send := func(ev sseEvent) bool {
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.name, ev.data); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if first != nil && !send(sseEvent{"state", first}) {
		return
	}
	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev := <-sub.events:
			if want != "" && !strings.Contains(string(ev.data), `"robot":"`+want+`"`) {
				continue
			}
			if !send(ev) {
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

/* --------------------------------- pairing -------------------------------- */

// handlePair is the QR target: the one-time code proves the browser can see the robot.
func (a *App) handlePair(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	code := strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(r.URL.Query().Get("code")))
	a.mu.Lock()
	rb, ok := a.redeem(sid, code)
	if ok {
		if c := rb.conn; c != nil {
			c.frame(wire.KindPaired, wire.PairedBody{Viewers: a.viewers(rb.id)})
			c.frame(wire.KindPairCode, a.issueCode(rb.id)) // the QR on screen stays valid for the next one
		}
		a.publishState(rb)
	}
	a.mu.Unlock()
	if !ok {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1">`+
			`<title>QR</title><body style="font-family:system-ui;padding:24px;font-size:20px">`+
			`<p>Tento kód už neplatí. Naskenuj QR kód na robotovi znovu.</p>`+
			`<p>This code has expired. Scan the QR code on the robot again.</p>`)
		return
	}
	a.log.Info("browser paired", "robot", rb.id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

/* ----------------------------------- kid ---------------------------------- */

func (a *App) handleState(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.pairedRobot(sid, r.URL.Query().Get("robot"))
	if rb == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_paired"})
		return
	}
	writeJSON(w, http.StatusOK, a.view(rb))
}

// handleAction is a button on the kid's page: {"action": "feed|cuddle|play|nap|wake|needs", "food"}.
func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	var req struct {
		Robot  string `json:"robot"`
		Action string `json:"action"`
		Food   string `json:"food"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.pairedRobot(sid, req.Robot)
	if rb == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_paired"})
		return
	}
	now := a.now()
	p := rb.pet
	var re pet.Reaction
	switch req.Action {
	case "feed":
		re = p.Feed(now, req.Food)
	case "cuddle":
		re = p.Cuddle(now)
	case "play": // a game of catch on the robot; a dance without it
		re = a.playAction(rb, now)
	case "nap":
		re = p.Nap(now)
	case "wake":
		re = p.Wake(now)
	case "needs": // show the needs on the robot's screen
		p.Advance(now)
		a.showNeeds(rb, now)
		writeJSON(w, http.StatusOK, map[string]any{"reaction": pet.Reaction{Kind: "needs"}, "state": a.view(rb)})
		return
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	a.dirty = true
	a.react(rb, re, now)
	a.publishReaction(rb, re)
	a.publishState(rb)
	writeJSON(w, http.StatusOK, map[string]any{"reaction": re, "state": a.view(rb)})
}

/* --------------------------------- parent --------------------------------- */

const (
	parentUnlockTime = 30 * time.Minute
	pinTries         = 5
	pinLockout       = time.Minute
)

var pinPattern = regexp.MustCompile(`^[0-9]{4,8}$`)

func hashPIN(pin string) string {
	salt := make([]byte, 16)
	rand.Read(salt)
	return pinHashWith(hex.EncodeToString(salt), pin)
}

func pinHashWith(salt, pin string) string {
	sum := sha256.Sum256([]byte(salt + ":" + pin))
	return salt + ":" + hex.EncodeToString(sum[:])
}

func pinOK(stored, pin string) bool {
	salt, _, ok := strings.Cut(stored, ":")
	return ok && subtle.ConstantTimeCompare([]byte(pinHashWith(salt, pin)), []byte(stored)) == 1
}

// parentRobot is the session's robot when the parent page is unlocked. a.mu held.
func (a *App) parentRobot(w http.ResponseWriter, r *http.Request, sid string) *robot {
	rb := a.pairedRobot(sid, r.URL.Query().Get("robot"))
	if rb == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_paired"})
		return nil
	}
	if s := a.sessions[sid]; s == nil || !a.cfg.Now().Before(s.parentUntil) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "locked"})
		return nil
	}
	return rb
}

type tagView struct {
	UID  string    `json:"uid"`
	Seen time.Time `json:"seen"`
}

// handleParent: whether a PIN is set and the page is unlocked; everything else once unlocked.
func (a *App) handleParent(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.pairedRobot(sid, r.URL.Query().Get("robot"))
	if rb == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_paired"})
		return
	}
	s := a.sessionFor(sid)
	unlocked := a.cfg.Now().Before(s.parentUntil)
	out := map[string]any{"has_pin": rb.pinHash != "", "unlocked": unlocked, "lang": rb.pet.Settings.Lang}
	if unlocked {
		now := a.now()
		p := rb.pet
		p.Advance(now)
		log := append([]pet.Entry{}, p.Log...) // [] not null for a new pet
		slices.Reverse(log)
		if len(log) > 100 {
			log = log[:100]
		}
		tags := []tagView{}
		for uid, seen := range rb.unknownTags {
			tags = append(tags, tagView{uid, seen})
		}
		slices.SortFunc(tags, func(x, y tagView) int { return y.Seen.Compare(x.Seen) })
		out["settings"] = p.Settings
		out["state"] = a.view(rb)
		out["played_today"] = p.PlayedToday(now)
		out["log"] = log
		out["unknown_tags"] = tags
		out["born"] = p.Born
		out["unlocked_until"] = s.parentUntil
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUnlock checks the PIN; the first PIN ever entered becomes the PIN.
func (a *App) handleUnlock(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	var req struct {
		PIN string `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.pairedRobot(sid, r.URL.Query().Get("robot"))
	if rb == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_paired"})
		return
	}
	s := a.sessionFor(sid)
	now := a.cfg.Now()
	if s.pinFails >= pinTries && now.Sub(s.pinFailAt) < pinLockout {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "wait"})
		return
	}
	if !pinPattern.MatchString(req.PIN) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pin_format"})
		return
	}
	if rb.pinHash == "" {
		rb.pinHash = hashPIN(req.PIN)
		a.dirty = true
		a.log.Info("parent PIN set", "robot", rb.id)
	} else if !pinOK(rb.pinHash, req.PIN) {
		if now.Sub(s.pinFailAt) >= pinLockout {
			s.pinFails = 0
		}
		s.pinFails++
		s.pinFailAt = now
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "wrong_pin"})
		return
	}
	s.pinFails = 0
	s.parentUntil = now.Add(parentUnlockTime)
	writeJSON(w, http.StatusOK, map[string]bool{"unlocked": true})
}

func (a *App) handleLock(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	a.mu.Lock()
	a.sessionFor(sid).parentUntil = time.Time{}
	a.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]bool{"unlocked": false})
}

// handleSettings replaces the settings (the parent page sends all of them).
func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	var s pet.Settings
	if !readJSON(w, r, &s) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.parentRobot(w, r, sid)
	if rb == nil {
		return
	}
	now := a.now()
	rb.pet.Advance(now) // the old schedule up to now
	s.Normalize()
	for uid := range s.Foods {
		delete(rb.unknownTags, uid)
	}
	oldLang := rb.pet.Settings.Lang
	rb.pet.Settings = s
	if s.Lang != oldLang {
		go a.warmVoice(context.Background()) // the new language's lines
	}
	rb.phase = rb.pet.Phase(now) // a new bedtime starts tonight's routine, not instantly
	a.dirty = true
	if c := rb.conn; c != nil {
		if s.Sounds {
			c.command("volume", map[string]any{"value": s.Volume})
		}
		if now.After(rb.busyUntil) {
			if rb.phase == pet.Night {
				a.express(rb, now)
				a.dimForNight(rb, now)
			} else {
				a.express(rb, now)
			}
		}
	}
	a.publishState(rb)
	writeJSON(w, http.StatusOK, map[string]any{"settings": s})
}

func (a *App) handlePIN(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	var req struct {
		PIN string `json:"pin"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.parentRobot(w, r, sid)
	if rb == nil {
		return
	}
	if !pinPattern.MatchString(req.PIN) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pin_format"})
		return
	}
	rb.pinHash = hashPIN(req.PIN)
	a.dirty = true
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleReset starts a new pet with the same settings.
func (a *App) handleReset(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.parentRobot(w, r, sid)
	if rb == nil {
		return
	}
	now := a.now()
	rb.pet = pet.New(now, rb.pet.Settings)
	rb.pet.Note(now, "reset", "")
	a.dirty = true
	a.express(rb, now)
	a.publishState(rb)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleStats lets a parent set the needs: {"food", "fun", "energy"} (0..100; missing ones stay).
func (a *App) handleStats(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	var req struct {
		Food, Fun, Energy *float64
	}
	if !readJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.parentRobot(w, r, sid)
	if rb == nil {
		return
	}
	now := a.now()
	p := rb.pet
	p.Advance(now)
	set := func(dst *float64, v *float64) {
		if v != nil {
			*dst = max(0, min(100, *v))
		}
	}
	set(&p.Stats.Food, req.Food)
	set(&p.Stats.Fun, req.Fun)
	set(&p.Stats.Energy, req.Energy)
	p.Note(now, "parent_stats", fmt.Sprintf("%.0f/%.0f/%.0f", p.Stats.Food, p.Stats.Fun, p.Stats.Energy))
	a.dirty = true
	if rb.conn != nil && now.After(rb.busyUntil) && rb.game == nil {
		a.express(rb, now)
	}
	a.publishState(rb)
	writeJSON(w, http.StatusOK, map[string]any{"stats": p.Stats})
}

// handleTry lets a parent preview: {"what": "morning|night|needs|sound:<name>"}.
func (a *App) handleTry(w http.ResponseWriter, r *http.Request) {
	sid := a.sessionID(w, r)
	var req struct {
		What string `json:"what"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.parentRobot(w, r, sid)
	if rb == nil {
		return
	}
	if rb.conn == nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "offline"})
		return
	}
	now := a.now()
	switch name, isSound := strings.CutPrefix(req.What, "sound:"); {
	case isSound:
		saved := rb.pet.Settings.Sounds
		rb.pet.Settings.Sounds = true // a preview plays even with sounds off
		a.play(rb, name, true)
		rb.pet.Settings.Sounds = saved
	case req.What == "morning":
		a.morning(rb, now)
	case req.What == "night":
		a.goodnight(rb, now)
	case req.What == "needs":
		a.showNeeds(rb, now)
	case req.What == "voice": // a spoken line, even at night (a preview)
		saved := rb.pet.Settings.Sounds
		rb.pet.Settings.Sounds = true
		a.sayLine(rb, text(rb.pet.Settings.Lang, "hungry", ""), 3, true)
		rb.pet.Settings.Sounds = saved
	default:
		http.Error(w, "unknown preview", http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

/* --------------------------------- helpers -------------------------------- */

// readJSON decodes a small JSON body. Requiring JSON forces a CORS preflight
// for cross-site requests, so another site cannot press buttons for the kid.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return false
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(v); err != nil {
		http.Error(w, "bad JSON: "+err.Error(), http.StatusBadRequest)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
