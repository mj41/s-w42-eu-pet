package app

import (
	"testing"
	"time"

	"github.com/mj41/stackchan-server/wire"
)

// robotState runs fn on the robot under the App's lock.
func (e *env) robotState(id string, fn func(r *robot)) {
	e.app.mu.Lock()
	defer e.app.mu.Unlock()
	fn(e.app.robots[id])
}

func TestWatchdogKeepsTheFaceOnByDay(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	r.frame(wire.KindAccepted)
	var sent uint64
	count := func() { e.robotState("robot-1", func(rb *robot) { sent = rb.conn.sent.Load() }) }
	e.app.Pulse() // the watchdog notes the commands sent on connecting
	count()
	e.clock.set(e.clock.now().Add(keepAlive - time.Second))
	e.app.Pulse()
	e.robotState("robot-1", func(rb *robot) {
		if rb.conn.sent.Load() != sent {
			t.Fatal("the face was shown again too soon")
		}
	})
	e.clock.set(e.clock.now().Add(2 * time.Second))
	e.app.Pulse()
	e.robotState("robot-1", func(rb *robot) {
		if rb.conn.sent.Load() == sent {
			t.Fatal("no keep-alive before the robot's 60 s screensaver")
		}
	})

	// A double tap blanked the screen on purpose: it stays dark.
	r.event("screensaver_on", map[string]any{"manual": 1})
	time.Sleep(50 * time.Millisecond)
	count()
	e.clock.set(e.clock.now().Add(2 * keepAlive))
	e.app.Pulse()
	e.robotState("robot-1", func(rb *robot) {
		if n := rb.conn.sent.Load(); n != sent {
			t.Fatalf("%d commands sent to a screen blanked on purpose", n-sent)
		}
	})
}

func TestWatchdogEndsStuckStates(t *testing.T) {
	e := newEnv(t, "")
	r := e.connectRobot("robot-1")
	r.frame(wire.KindAccepted)
	now := e.clock.now()
	cases := []struct {
		name  string
		setup func(rb *robot)
		ok    func(rb *robot) bool
	}{
		{"menu", func(rb *robot) { rb.menu, rb.busyUntil = "main", now.Add(-time.Minute) },
			func(rb *robot) bool { return rb.menu == "" }},
		{"game", func(rb *robot) { rb.game, rb.busyUntil = &game{}, now.Add(-time.Second) },
			func(rb *robot) bool { return rb.game == nil }},
		{"busy", func(rb *robot) { rb.busyUntil = now.Add(time.Hour) },
			func(rb *robot) bool { return rb.busyUntil.IsZero() }},
		{"picture", func(rb *robot) { rb.pictureOn, rb.busyUntil = true, now.Add(-time.Minute) },
			func(rb *robot) bool { return !rb.pictureOn }},
	}
	for _, c := range cases {
		e.robotState("robot-1", c.setup)
		e.app.Pulse()
		r.command("emotion") // the mood again
		e.robotState("robot-1", func(rb *robot) {
			if !c.ok(rb) {
				t.Errorf("%s: still stuck", c.name)
			}
		})
	}
}
