package app

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/mj41/s-w42-eu-raw/wire"
)

// Liveness timing, as in s-w42-eu-raw.
const (
	pingPeriod      = 5 * time.Second
	pongWait        = 60 * time.Second
	writeWait       = 10 * time.Second
	registerTimeout = 10 * time.Second
	maxMessageBytes = 256 << 10
)

var robotIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// Robots authenticate with a bearer token, not cookies.
	CheckOrigin: func(r *http.Request) bool { return true },
}

type outMsg struct {
	binary bool
	data   []byte
}

// robotConn is one robot's socket; the writer goroutine owns all writes.
type robotConn struct {
	id   string
	ws   *websocket.Conn
	send chan outMsg

	closeOnce sync.Once
	done      chan struct{}

	sent, dropped atomic.Uint64 // messages queued, and dropped because the queue was full (watchdog.go)
	held          atomic.Bool   // the robot is in someone's hands: no head commands (held.go)

	mgrToken    string       // a token from the manager: asked about again every minute (managed.go)
	managedSent atomic.Int32 // the version of the signed app list relayed last
	firmware    string       // from Register, for the manager (owner's page)
	appsVersion atomic.Int32 // the app list version the robot has (label apps_ver, then AppsVersion)
}

func (c *robotConn) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.ws.Close()
	})
}

func (c *robotConn) queue(m outMsg) bool {
	select {
	case <-c.done:
		return false
	case c.send <- m:
		c.sent.Add(1)
		return true
	default:
		c.dropped.Add(1)
		return false
	}
}

// frame queues a JSON frame.
func (c *robotConn) frame(kind string, body any) bool {
	f, err := wire.Marshal(kind, wire.Meta{WorkerID: c.id}, body)
	return err == nil && c.queue(outMsg{data: f})
}

// command queues a robot command.
func (c *robotConn) command(name string, args map[string]any) bool {
	if headCommands[name] && c.held.Load() {
		return false // the head stays still in a kid's hands
	}
	return c.frame(wire.KindRobotCommand, wire.RobotCommandBody{Command: name, Args: args})
}

// binary queues a binary message: type byte, then payload.
func (c *robotConn) binary(kind byte, payload []byte) bool {
	return c.queue(outMsg{binary: true, data: append([]byte{kind}, payload...)})
}

func (a *App) handleRobotConnect(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	id := wire.DeviceID(r.Header)
	if !robotIDPattern.MatchString(id) {
		http.Error(w, "missing or invalid "+wire.DeviceIDHeader, http.StatusBadRequest)
		return
	}
	if !a.robotOK(r.Context(), id, token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		a.log.Warn("robot upgrade failed", "robot", id, "err", err)
		return
	}
	ws.SetReadLimit(maxMessageBytes)
	c := &robotConn{id: id, ws: ws, send: make(chan outMsg, 64), done: make(chan struct{})}
	if a.cfg.Manager != nil && !a.tokenOK(token) {
		c.mgrToken = token
	}
	defer c.close()

	reg, reason := readRegister(ws, id)
	if reason != "" {
		a.log.Warn("robot rejected", "robot", id, "reason", reason)
		if f, err := wire.Marshal(wire.KindRejected, wire.Meta{}, wire.RejectedBody{Reason: reason}); err == nil {
			ws.SetWriteDeadline(time.Now().Add(writeWait))
			ws.WriteMessage(websocket.TextMessage, f)
		}
		return
	}
	a.log.Info("robot connected", "robot", id, "firmware", reg.Capabilities.Firmware)
	defer a.log.Info("robot disconnected", "robot", id)

	c.firmware = reg.Capabilities.Firmware
	if v, err := strconv.Atoi(reg.Labels["apps_ver"]); err == nil {
		c.appsVersion.Store(int32(v))
	}
	a.attach(c, reg.Capabilities.Commands)
	defer a.detach(c)
	a.relayManaged(r.Context(), c)
	go a.writeLoop(c)
	a.readLoop(c)
}

func readRegister(ws *websocket.Conn, id string) (wire.RegisterBody, string) {
	var reg wire.RegisterBody
	ws.SetReadDeadline(time.Now().Add(registerTimeout))
	_, data, err := ws.ReadMessage()
	if err != nil {
		return reg, "no Register frame"
	}
	frames, err := wire.Parse(data)
	if err != nil || len(frames) == 0 || frames[0].Kind != wire.KindRegister {
		return reg, "first frame must be Register"
	}
	f := frames[0]
	if f.Meta.WorkerID != "" && f.Meta.WorkerID != id {
		return reg, "meta.worker_id does not match " + wire.DeviceIDHeader
	}
	if err := f.Decode(&reg); err != nil {
		return reg, "invalid Register body"
	}
	if reg.Class != wire.ClassRobot {
		return reg, "unknown worker class"
	}
	return reg, ""
}

// attach makes c the robot's connection: accepted, a pairing code, and the pet's face.
func (a *App) attach(c *robotConn, commands []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robotFor(c.id)
	old := r.conn
	r.conn = c
	c.held.Store(r.hold.held)
	r.commands = commands
	r.files = map[string]bool{} // filled from the robot's "assets" answer
	r.spriteIDs, r.faceShown, r.faceHidden = nil, "", false
	r.lastPlay, r.idleOff = a.cfg.Now(), false // just connected: the full time before the screen goes off
	if slices.Contains(commands, "sprite") {
		c.command("sprite_clear", nil) // sprites stay on the robot across servers
	}
	r.lastSeen = a.cfg.Now()
	if old != nil {
		old.close()
	}
	c.frame(wire.KindAccepted, nil)
	c.command("light_stream", map[string]any{"on": false}) // in case a game was cut short
	c.command("assets", nil)                               // what the robot's file store holds (logged; used later for the pet's pictures)
	c.frame(wire.KindPairCode, a.issueCode(c.id))
	// Browsers paired before: the robot starts with its face, not the QR screen.
	if n := a.viewers(c.id); n > 0 {
		c.frame(wire.KindPaired, wire.PairedBody{Viewers: n, Reconnect: true})
	}
	a.robotOnline(r)
	a.publishState(r)
}

func (a *App) detach(c *robotConn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robots[c.id]
	if r == nil || r.conn != c {
		return
	}
	r.conn = nil
	r.game = nil // the robot drops its streams when it switches servers or reconnects
	for code, pc := range a.codes {
		if pc.robotID == c.id {
			delete(a.codes, code)
		}
	}
	a.publishState(r)
}

// writeLoop owns all writes: queued messages, pings, fresh pairing codes.
func (a *App) writeLoop(c *robotConn) {
	ping := time.NewTicker(pingPeriod)
	defer ping.Stop()
	rotate := time.NewTicker(a.cfg.PairTTL)
	defer rotate.Stop()
	for {
		select {
		case <-c.done:
			return
		case m := <-c.send:
			kind := websocket.TextMessage
			if m.binary {
				kind = websocket.BinaryMessage
			}
			c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(kind, m.data); err != nil {
				c.close()
				return
			}
		case <-ping.C:
			if err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(writeWait)); err != nil {
				c.close()
				return
			}
		case <-rotate.C:
			a.mu.Lock()
			body := a.issueCode(c.id)
			a.mu.Unlock()
			c.frame(wire.KindPairCode, body)
		}
	}
}

func (a *App) readLoop(c *robotConn) {
	resetDeadline := func() { c.ws.SetReadDeadline(time.Now().Add(pongWait)) }
	resetDeadline()
	c.ws.SetPongHandler(func(string) error { resetDeadline(); return nil })
	c.ws.SetPingHandler(func(data string) error {
		resetDeadline()
		return c.ws.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(writeWait))
	})
	for {
		kind, data, err := c.ws.ReadMessage()
		if err != nil {
			return
		}
		resetDeadline()
		if kind == websocket.BinaryMessage {
			if len(data) > 1 && data[0] == wire.BinLight { // the game's hand detection
				a.lightSamples(c.id, data[1:])
			}
			if len(data) > 1 && data[0] == wire.BinSnapshot { // a leaderboard photo, or a screen snapshot (photos.go)
				a.robotSnapshot(c.id, data[1:])
			}
			if len(data) > 1 && data[0] == wire.BinAudioMulti { // a debug recording (debug.go)
				a.micAudio(c.id, data[1:])
			}
			continue // the pet asks for no camera or microphone
		}
		frames, err := wire.Parse(data)
		if err != nil {
			a.log.Warn("bad frame from robot", "robot", c.id, "err", err)
			continue
		}
		for _, f := range frames {
			if f.Kind == wire.KindRobotTelemetry {
				var t wire.RobotTelemetryBody
				if err := f.Decode(&t); err != nil {
					a.log.Debug("bad telemetry", "robot", c.id, "err", err, "body", string(f.Body))
				} else {
					a.telemetry(c.id, t.Measurements)
				}
				continue
			}
			if f.Kind == wire.KindAppsVersion {
				var body wire.AppsVersionBody
				if f.Decode(&body) == nil && body.Version > 0 {
					c.appsVersion.Store(body.Version)
					go a.relayManaged(context.Background(), c) // the manager learns at once
				}
				continue
			}
			if f.Kind != wire.KindRobotEvent {
				continue // heartbeats: the socket being alive is enough
			}
			var ev wire.RobotEventBody
			if err := f.Decode(&ev); err != nil || ev.Name == "" {
				continue
			}
			a.robotEvent(c.id, ev)
		}
	}
}
