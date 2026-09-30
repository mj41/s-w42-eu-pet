package app

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mj41/stackchan-server/wire"
)

const testToken = "test-token"

var prague, _ = time.LoadLocation("Europe/Prague")

// clock is a settable time for the App.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

type env struct {
	t     *testing.T
	app   *App
	srv   *httptest.Server
	clock *clock
	state string
}

func newEnv(t *testing.T, stateFile string) *env {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, prague)} // Wednesday morning
	e := &env{t: t, clock: c, state: stateFile}
	e.app = New(Config{RobotToken: testToken, StateFile: stateFile, Location: prague, Now: c.now})
	e.srv = httptest.NewServer(e.app.Handler())
	e.app.cfg.PublicURL = e.srv.URL
	t.Cleanup(e.srv.Close)
	return e
}

// fakeRobot is a websocket client speaking the robot side of the protocol.
type fakeRobot struct {
	t    *testing.T
	ws   *websocket.Conn
	msgs chan robotMsg

	mu       sync.Mutex
	pairCode wire.PairCodeBody // the latest, kept aside: waiting for other messages skips frames
}

type robotMsg struct {
	frame  wire.Frame
	binary []byte
}

func (e *env) connectRobot(id string) *fakeRobot {
	e.t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+testToken)
	h.Set(wire.WorkerIDHeader, id)
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(e.srv.URL, "http")+wire.ConnectPath, h)
	if err != nil {
		e.t.Fatalf("robot dial: %v", err)
	}
	e.t.Cleanup(func() { ws.Close() })
	f, _ := wire.Marshal(wire.KindRegister, wire.Meta{WorkerID: id}, wire.RegisterBody{Class: wire.ClassRobot})
	ws.WriteMessage(websocket.TextMessage, f)
	r := &fakeRobot{t: e.t, ws: ws, msgs: make(chan robotMsg, 256)}
	go func() {
		for {
			kind, data, err := ws.ReadMessage()
			if err != nil {
				close(r.msgs)
				return
			}
			if kind == websocket.BinaryMessage {
				r.msgs <- robotMsg{binary: data}
				continue
			}
			frames, _ := wire.Parse(data)
			for _, fr := range frames {
				if fr.Kind == wire.KindPairCode {
					r.mu.Lock()
					fr.Decode(&r.pairCode)
					r.mu.Unlock()
				}
				r.msgs <- robotMsg{frame: fr}
			}
		}
	}()
	return r
}

// next waits for a message matching ok, skipping others.
func (r *fakeRobot) next(what string, ok func(robotMsg) bool) robotMsg {
	r.t.Helper()
	timeout := time.After(3 * time.Second)
	for {
		select {
		case m, open := <-r.msgs:
			if !open {
				r.t.Fatalf("robot socket closed waiting for %s", what)
			}
			if ok(m) {
				return m
			}
		case <-timeout:
			r.t.Fatalf("timeout waiting for %s", what)
		}
	}
}

func (r *fakeRobot) frame(kind string) wire.Frame {
	r.t.Helper()
	return r.next(kind, func(m robotMsg) bool { return m.frame.Kind == kind }).frame
}

// command waits for a RobotCommand and returns its args.
func (r *fakeRobot) command(name string) map[string]any {
	r.t.Helper()
	var body wire.RobotCommandBody
	r.next("command "+name, func(m robotMsg) bool {
		if m.frame.Kind != wire.KindRobotCommand {
			return false
		}
		body = wire.RobotCommandBody{}
		m.frame.Decode(&body)
		return body.Command == name
	})
	return body.Args
}

func (r *fakeRobot) binary(kind byte) []byte {
	r.t.Helper()
	return r.next("binary", func(m robotMsg) bool { return len(m.binary) > 0 && m.binary[0] == kind }).binary
}

func (r *fakeRobot) event(name string, data map[string]any) {
	f, _ := wire.Marshal(wire.KindRobotEvent, wire.Meta{}, wire.RobotEventBody{Name: name, Data: data})
	r.ws.WriteMessage(websocket.TextMessage, f)
}

// browser is an HTTP client with its own cookie jar.
type browser struct {
	t *testing.T
	c *http.Client
	e *env
}

func (e *env) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: e.t, e: e, c: &http.Client{Jar: jar}}
}

func (b *browser) get(path string) (int, map[string]any) {
	b.t.Helper()
	res, err := b.c.Get(b.e.srv.URL + path)
	if err != nil {
		b.t.Fatal(err)
	}
	return decode(b.t, res)
}

func (b *browser) post(path string, body any) (int, map[string]any) {
	b.t.Helper()
	j, _ := json.Marshal(body)
	res, err := b.c.Post(b.e.srv.URL+path, "application/json", bytes.NewReader(j))
	if err != nil {
		b.t.Fatal(err)
	}
	return decode(b.t, res)
}

func decode(t *testing.T, res *http.Response) (int, map[string]any) {
	defer res.Body.Close()
	raw, _ := io.ReadAll(res.Body)
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	return res.StatusCode, out
}

// pair scans the robot's current code.
func (b *browser) pair(r *fakeRobot) {
	b.t.Helper()
	var pc wire.PairCodeBody
	for deadline := time.Now().Add(3 * time.Second); pc.URL == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		r.mu.Lock()
		pc = r.pairCode
		r.mu.Unlock()
	}
	res, err := b.c.Get(pc.URL)
	if err != nil || res.StatusCode != http.StatusOK {
		b.t.Fatalf("pair: %v %v", err, res.Status)
	}
	res.Body.Close()
}

func stats(t *testing.T, state map[string]any) map[string]any {
	t.Helper()
	s, ok := state["stats"].(map[string]any)
	if !ok {
		t.Fatalf("no stats in %v", state)
	}
	return s
}

func TestRobotNeedsToken(t *testing.T) {
	e := newEnv(t, "")
	req, _ := http.NewRequest("GET", e.srv.URL+wire.ConnectPath, nil)
	req.Header.Set("Authorization", "Bearer nope")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v %v", err, res.Status)
	}
}

func TestPairAndPlay(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	r.frame(wire.KindAccepted)
	r.command("emotion") // the pet's face right away

	kid := e.browser()
	if code, out := kid.get("/api/state"); code != http.StatusNotFound || out["error"] != "not_paired" {
		t.Fatalf("unpaired state: %d %v", code, out)
	}
	kid.pair(r)
	r.frame(wire.KindPaired)

	code, st := kid.get("/api/state")
	if code != 200 || st["robot"] != "robot-1" || st["online"] != true || st["phase"] != "awake" {
		t.Fatalf("state: %d %v", code, st)
	}

	// Feeding from the page: food picture and a munch on the robot.
	code, out := kid.post("/api/action", map[string]any{"action": "feed", "food": "cake"})
	if code != 200 || out["reaction"].(map[string]any)["kind"] != "eat" {
		t.Fatalf("feed: %d %v", code, out)
	}
	if pic := r.binary(wire.BinShowJPEG); len(pic) < 1000 {
		t.Fatalf("food picture: %d bytes", len(pic))
	}
	if pcm := r.binary(wire.BinSpeakerPCM); len(pcm) < 1000 {
		t.Fatalf("munch: %d bytes", len(pcm))
	}
	r.command("face") // after the picture
	if say := r.command("say"); !strings.Contains(say["text"].(string), "ortik") && !strings.Contains(say["text"].(string), "Mnam") {
		t.Fatalf("eat bubble: %v", say)
	}

	// Stroking the head is a cuddle.
	fun := stats(t, out["state"].(map[string]any))["fun"].(float64)
	r.event("head_press", map[string]any{"z0": 2})
	r.command("sticker")
	_, st = kid.get("/api/state")
	if got := stats(t, st)["fun"].(float64); got != fun+6 {
		t.Fatalf("fun after cuddle %.1f, want %.1f", got, fun+6)
	}

	// Another browser without pairing sees nothing and cannot act.
	stranger := e.browser()
	if code, _ := stranger.post("/api/action", map[string]any{"action": "play"}); code != http.StatusNotFound {
		t.Fatalf("stranger action: %d", code)
	}
}

func TestFoodCardsAndParent(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	parent := e.browser()
	parent.pair(r)
	other := e.browser() // for the PIN lockout below; codes expire, so pair before the clock moves
	other.pair(r)

	// An unknown card feeds an apple and is remembered for the parent.
	r.event("nfc_tag", map[string]any{"uid": "04A1B2C3"})
	r.binary(wire.BinShowJPEG)

	if code, out := parent.get("/api/parent"); code != 200 || out["unlocked"] != false || out["has_pin"] != false {
		t.Fatalf("locked parent page: %d %v", code, out)
	}
	if code, _ := parent.post("/api/parent/settings", map[string]any{}); code != http.StatusForbidden {
		t.Fatalf("settings while locked: %d", code)
	}
	if code, _ := parent.post("/api/parent/unlock", map[string]any{"pin": "12"}); code != http.StatusBadRequest {
		t.Fatalf("short PIN: %d", code)
	}
	if code, _ := parent.post("/api/parent/unlock", map[string]any{"pin": "4321"}); code != 200 {
		t.Fatalf("set PIN: %d", code)
	}
	code, out := parent.get("/api/parent")
	tags := out["unknown_tags"].([]any)
	if code != 200 || len(tags) != 1 || tags[0].(map[string]any)["uid"] != "04A1B2C3" {
		t.Fatalf("unknown tags: %d %v", code, out["unknown_tags"])
	}
	log := out["log"].([]any)
	if len(log) == 0 || log[0].(map[string]any)["detail"] != "apple" {
		t.Fatalf("log: %v", log)
	}

	// Assign the card to milk; bedtime and language too.
	s := out["settings"].(map[string]any)
	s["foods"] = map[string]any{"04A1B2C3": "milk"}
	s["lang"] = "en"
	s["weekday"] = map[string]any{"wake": "07:00", "bed": "21:00"}
	if code, _ := parent.post("/api/parent/settings", s); code != 200 {
		t.Fatalf("save settings: %d", code)
	}
	_, out = parent.get("/api/parent")
	if len(out["unknown_tags"].([]any)) != 0 || out["settings"].(map[string]any)["lang"] != "en" {
		t.Fatalf("after saving: %v", out)
	}
	e.clock.set(e.clock.now().Add(time.Hour)) // hungry enough to eat again
	r.event("nfc_tag", map[string]any{"uid": "04A1B2C3"})
	if say := r.command("say"); !strings.Contains(say["text"].(string), "ilk") && !strings.Contains(say["text"].(string), "om") && !strings.Contains(say["text"].(string), "elicious") {
		t.Fatalf("milk bubble: %v", say)
	}

	// Wrong PINs lock out after a few tries, in another browser too.
	for i := 0; i < pinTries; i++ {
		if code, _ := other.post("/api/parent/unlock", map[string]any{"pin": "0000"}); code != http.StatusForbidden {
			t.Fatalf("wrong PIN %d: %d", i, code)
		}
	}
	if code, _ := other.post("/api/parent/unlock", map[string]any{"pin": "4321"}); code != http.StatusTooManyRequests {
		t.Fatalf("after lockout: %d", code)
	}
}

func TestBedtimeAndMorning(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)

	e.clock.set(time.Date(2026, 9, 30, 19, 55, 0, 0, prague)) // 5 minutes before bed
	e.app.Tick()
	if say := r.command("say"); !strings.Contains(say["text"].(string), "spat") {
		t.Fatalf("bedtime warning: %v", say)
	}
	e.clock.set(time.Date(2026, 9, 30, 20, 0, 30, 0, prague))
	e.app.Tick()
	if say := r.command("say"); !strings.Contains(say["text"].(string), "Dobrou noc") {
		t.Fatalf("good night: %v", say)
	}
	r.binary(wire.BinSpeakerPCM) // the lullaby

	_, st := kid.get("/api/state")
	if st["phase"] != "night" || st["mood"] != "sleeping" {
		t.Fatalf("night state: %v", st)
	}
	if code, out := kid.post("/api/action", map[string]any{"action": "play"}); code != 200 || out["reaction"].(map[string]any)["kind"] != "asleep" {
		t.Fatalf("play at night: %v", out)
	}

	e.clock.set(time.Date(2026, 10, 1, 7, 0, 10, 0, prague))
	e.app.Tick()
	if on := r.command("screensaver"); on["on"] != false {
		t.Fatalf("morning screen: %v", on)
	}
	if say := r.command("say"); !strings.Contains(say["text"].(string), "Dobre rano") {
		t.Fatalf("morning: %v", say)
	}
}

func TestStateSurvivesRestart(t *testing.T) {
	file := filepath.Join(t.TempDir(), "state.json")
	e := newEnv(t, file)
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)
	kid.post("/api/parent/unlock", map[string]any{"pin": "1234"})
	kid.post("/api/action", map[string]any{"action": "feed", "food": "bread"})
	if err := e.app.Save(); err != nil {
		t.Fatal(err)
	}

	again := New(Config{RobotToken: testToken, StateFile: file, Location: prague, Now: e.clock.now})
	again.mu.Lock()
	defer again.mu.Unlock()
	rb := again.robots["robot-1"]
	if rb == nil || rb.pinHash == "" || !pinOK(rb.pinHash, "1234") || len(rb.pet.Log) == 0 || len(again.sessions) != 1 {
		t.Fatalf("after restart: %+v sessions %d", rb, len(again.sessions))
	}
}

func TestCatchTheBall(t *testing.T) {
	gameRoundTime, gameGap, gameIntro, gameStars = 300*time.Millisecond, 50*time.Millisecond, 50*time.Millisecond, 100*time.Millisecond
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	kid := e.browser()
	kid.pair(r)

	code, out := kid.post("/api/action", map[string]any{"action": "play"})
	if code != 200 || out["reaction"].(map[string]any)["kind"] != "game" {
		t.Fatalf("play: %d %v", code, out)
	}
	if say := r.command("say"); !strings.Contains(say["text"].(string), "micek") {
		t.Fatalf("game intro: %v", say)
	}
	r.binary(wire.BinShowJPEG) // the first ball

	// Tap the wrong quarter (ignored), then the ball.
	e.app.mu.Lock()
	spot := e.app.robots["robot-1"].game.spot
	e.app.mu.Unlock()
	centers := [][2]float64{{80, 60}, {240, 60}, {80, 180}, {240, 180}}
	wrong := centers[(spot+1)%4]
	r.event("screen_tap", map[string]any{"x": wrong[0], "y": wrong[1]})
	r.event("screen_tap", map[string]any{"x": centers[spot][0], "y": centers[spot][1]})
	r.binary(wire.BinSpeakerPCM) // the catch chirp (the intro hello came before the ball)

	// The other balls fly away unnoticed; then the stars and the score.
	r.next("game over", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		text, _ := body.Args["text"].(string)
		return body.Command == "say" && strings.Contains(text, "1 z 5")
	})
	_, st := kid.get("/api/state")
	if st["game"] != nil {
		t.Fatalf("game still on: %v", st["game"])
	}
	e.app.mu.Lock()
	last := e.app.robots["robot-1"].pet.Log
	e.app.mu.Unlock()
	if l := last[len(last)-1]; l.Kind != "play" || l.Detail != "1/5" {
		t.Fatalf("log: %+v", l)
	}
}
