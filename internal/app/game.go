package app

import (
	"fmt"
	"math/rand/v2"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
	"github.com/mj41/stackchan-pet/internal/robotpic"
	"github.com/mj41/stackchan-pet/internal/sound"
	"github.com/mj41/stackchan-server/wire"
)

// Catch the ball: the robot's screen shows a ball in one of its four quarters
// and the kid taps it. pet.GameRounds balls, a few seconds each; taps on the
// wrong quarter are simply ignored (no failing, just try again), and a ball
// not caught in time moves on. Everything here runs with a.mu held.

// Game timing (variables so tests can run a game quickly).
var (
	gameRoundTime = 5 * time.Second        // to catch one ball
	gameGap       = 700 * time.Millisecond // after a catch, before the next ball
	gameIntro     = 1500 * time.Millisecond
	gameStars     = 3 * time.Second // the result picture, before the face says the score
)

type game struct {
	round   int  // 1..pet.GameRounds, 0 before the first ball
	hits    int  // balls caught
	spot    int  // where the ball is (robotpic.SpotAt), -1 before the first
	waiting bool // the current ball can still be caught
}

// gameView is the game as the kid's page shows it.
type gameView struct {
	Round  int `json:"round"`
	Rounds int `json:"rounds"`
	Hits   int `json:"hits"`
}

// afterGame runs fn after d if g is still the robot's game.
func (a *App) afterGame(r *robot, g *game, d time.Duration, fn func()) {
	time.AfterFunc(d, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.game == g && r.conn != nil {
			fn()
		}
	})
}

// startGame begins a game on the robot (after pet.StartGame said yes).
func (a *App) startGame(r *robot, now time.Time) {
	r.gen++ // drop pending steps of earlier reactions
	g := &game{spot: -1}
	r.game = g
	r.busyUntil = now.Add(time.Minute)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "happy")
	a.say(r, "game", "", 2)
	a.play(r, sound.Hello, false)
	a.publishState(r)
	a.afterGame(r, g, gameIntro, func() { a.nextRound(r, g) })
}

func (a *App) nextRound(r *robot, g *game) {
	if g.round >= pet.GameRounds {
		a.endGame(r, g)
		return
	}
	g.round++
	spot := rand.IntN(robotpic.Spots)
	if g.spot >= 0 { // never the same place twice in a row
		spot = rand.IntN(robotpic.Spots - 1)
		if spot >= g.spot {
			spot++
		}
	}
	g.spot, g.waiting = spot, true
	r.conn.binary(wire.BinShowJPEG, robotpic.Ball(spot))
	r.pictureOn = true
	r.busyUntil = a.now().Add(time.Minute)
	a.publishState(r)
	round := g.round
	a.afterGame(r, g, gameRoundTime, func() {
		if g.round == round && g.waiting { // not caught: the next ball
			g.waiting = false
			a.nextRound(r, g)
		}
	})
}

// gameTap is a tap on the robot's screen during the game.
func (a *App) gameTap(r *robot, g *game, x, y float64, now time.Time) {
	if !g.waiting || robotpic.SpotAt(x, y) != g.spot {
		return
	}
	g.waiting = false
	g.hits++
	r.pet.Caught(now)
	a.play(r, sound.Chirp, false)
	leds := idleSides(r.pet, r.pet.Mood(now))
	leds["effect"], leds["color"], leds["speed"], leds["seconds"] = "blink", "#00ff40", 3, 1
	r.conn.command("leds", leds)
	a.publishState(r)
	round := g.round
	a.afterGame(r, g, gameGap, func() {
		if g.round == round {
			a.nextRound(r, g)
		}
	})
}

// endGame shows the stars, then the face says how it went.
func (a *App) endGame(r *robot, g *game) {
	now := a.now()
	r.game = nil
	re := r.pet.FinishGame(now, g.hits)
	a.dirty = true
	a.begin(r, now, 7*time.Second)
	r.conn.binary(wire.BinShowJPEG, robotpic.Stars(g.hits, pet.GameRounds))
	r.pictureOn = true
	a.play(r, sound.Tada, false)
	leds := idleSides(r.pet, r.pet.Mood(now))
	leds["effect"], leds["seconds"], leds["speed"] = "rainbow", 3, 2
	r.conn.command("leds", leds)
	a.later(r, gameStars, func() { // the speech bubble is under pictures: face first
		r.conn.command("face", nil)
		r.pictureOn = false
		a.emotion(r, "happy")
		key, score := "game_over", fmt.Sprintf(map[string]string{"cs": "%d z %d", "en": "%d of %d"}[r.pet.Settings.Lang], g.hits, pet.GameRounds)
		if g.hits == 0 {
			key = "game_over_0"
		}
		a.say(r, key, score, 4)
		r.conn.command("nod", nil)
	})
	a.publishReaction(r, re)
	a.publishState(r)
}

// stopGame drops a running game (bedtime, the robot left).
func (a *App) stopGame(r *robot) {
	if r.game != nil {
		r.game = nil
		a.publishState(r)
	}
}

// gameEvent handles a robot event during a game; false if the game does not care.
func (a *App) gameEvent(r *robot, ev wire.RobotEventBody, now time.Time) bool {
	g := r.game
	if g == nil {
		return false
	}
	switch ev.Name {
	case "screen_tap":
		x, _ := ev.Data["x"].(float64)
		y, _ := ev.Data["y"].(float64)
		a.gameTap(r, g, x, y, now)
	case "screensaver_on": // a quick double tap blanks the robot's screen: not now
		r.conn.command("screensaver", map[string]any{"on": false})
	}
	return true // other events wait until the game is over
}

// playAction starts a game on the robot, or plays without it when the robot is away.
func (a *App) playAction(r *robot, now time.Time) pet.Reaction {
	if r.game != nil {
		return pet.Reaction{Kind: pet.KindGame}
	}
	if r.conn == nil {
		return r.pet.Play(now)
	}
	re := r.pet.StartGame(now)
	if re.Kind == pet.KindGame {
		a.startGame(r, now)
	}
	return re
}

func (g *game) view() *gameView {
	if g == nil {
		return nil
	}
	return &gameView{Round: g.round, Rounds: pet.GameRounds, Hits: g.hits}
}
