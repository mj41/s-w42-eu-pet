package app

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
	"github.com/mj41/stackchan-pet/internal/robotpic"
	"github.com/mj41/stackchan-pet/internal/sound"
)

// The color game: the robot shows colors (small swatches at the top of its screen,
// its LEDs in the color to press next, and it says them), and the kid presses the
// same colors, in order, on four big buttons. pet.GameRounds rounds, longer and
// longer (colorsPerRound); a wrong button only buzzes, the clock runs on. At the
// end the robot says how long it took over all rounds; the best time is kept.
// It is a game like catch (r.game, with colors set). Everything here runs with
// a.mu held.

var colorsPerRound = [pet.GameRounds]int{1, 1, 2, 2, 3}

// colorRoundMax ends a round the kid does not finish; it counts with this time.
const colorRoundMax = 20 * time.Second

type colorGame struct {
	seq     []string // the colors to press this round, in order
	next    int      // the next one in seq
	started time.Time
	total   time.Duration // the rounds so far
	wrong   int
}

// Where the buttons are (centers on the 320x240 screen); the strip above shows the colors to press.
var colorButtons = map[string][2]int{"red": {82, 108}, "yellow": {238, 108}, "green": {82, 196}, "blue": {238, 196}}

// canColors: the robot has the game's pictures.
func (r *robot) canColors() bool {
	if !r.canSprite(assetDir + "menu-bg.png") {
		return false
	}
	for _, c := range robotpic.Colors {
		if !r.canSprite(assetDir + "color-" + c + ".png") {
			return false
		}
	}
	return true
}

// colorsAction starts the color game (from the robot's play menu).
func (a *App) colorsAction(r *robot, now time.Time) pet.Reaction {
	if r.game != nil {
		return pet.Reaction{Kind: pet.KindGame}
	}
	if r.conn == nil || !r.canColors() {
		return r.pet.Play(now)
	}
	re := r.pet.StartGame(now)
	if re.Kind == pet.KindGame {
		a.startColors(r, now)
	}
	return re
}

func (a *App) startColors(r *robot, now time.Time) {
	r.gen++ // drop pending steps of earlier reactions
	g := &game{colors: &colorGame{}}
	r.game = g
	r.menu = "" // the game replaces a menu (its sprites are cleared below)
	r.busyUntil = now.Add(time.Minute)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.rememberHead(r)
	a.clearSprites(r)
	a.emotion(r, "happy")
	a.sprite(r, map[string]any{"id": "c:bg", "asset": assetDir + "menu-bg.png", "x": 160, "y": 120, "z": 20})
	for c, at := range colorButtons {
		a.sprite(r, map[string]any{"id": "c:" + c, "asset": assetDir + "color-" + c + ".png", "x": at[0], "y": at[1], "z": 21, "tap": true})
	}
	a.say(r, "color_game", "", 2)
	a.play(r, sound.Hello, false)
	a.publishState(r)
	a.afterGame(r, g, a.timing.intro, func() { a.colorRound(r, g) })
}

// colorRound shows the next colors to press.
func (a *App) colorRound(r *robot, g *game) {
	cg := g.colors
	for i := range cg.seq {
		r.conn.command("sprite_hide", map[string]any{"id": fmt.Sprintf("c:s%d", i)})
	}
	if g.round >= pet.GameRounds {
		a.endColors(r, g)
		return
	}
	g.round++
	n := colorsPerRound[g.round-1]
	perm := rand.Perm(len(robotpic.Colors))
	cg.seq, cg.next = make([]string, n), 0
	for i := range n {
		cg.seq[i] = robotpic.Colors[perm[i]]
	}
	for i, c := range cg.seq { // small swatches in a row at the top
		x := 160 + (2*i-(n-1))*36
		a.sprite(r, map[string]any{"id": fmt.Sprintf("c:s%d", i), "asset": assetDir + "color-" + c + ".png",
			"x": x, "y": 32, "scale": 0.45, "z": 22, "opacity": 1.0, "hidden": false})
	}
	a.colorLEDs(r, cg.seq[0])
	a.sayText(r, colorPrompt(r.pet.Settings.Lang, cg.seq), 3)
	cg.started = time.Now()
	r.busyUntil = a.now().Add(time.Minute)
	a.publishState(r)
	round := g.round
	a.afterGame(r, g, colorRoundMax, func() {
		if g.round == round && cg.next < len(cg.seq) { // not finished: on to the next round
			cg.total += colorRoundMax
			a.colorRound(r, g)
		}
	})
}

// colorLEDs lights both LEDs in the color to press.
func (a *App) colorLEDs(r *robot, c string) {
	rgb := robotpic.ColorRGBA[c]
	hex := fmt.Sprintf("#%02x%02x%02x", rgb.R, rgb.G, rgb.B)
	r.conn.command("leds", map[string]any{"left": hex, "right": hex})
}

// colorTap is a tap on a button (sprite id "c:<color>") during the color game.
func (a *App) colorTap(r *robot, g *game, sprite string) {
	cg := g.colors
	c, ok := strings.CutPrefix(sprite, "c:")
	if _, button := colorButtons[c]; !ok || !button || cg.next >= len(cg.seq) {
		return
	}
	if c != cg.seq[cg.next] {
		cg.wrong++
		a.play(r, sound.No, false)
		leds := idleSides(r.pet, r.pet.Mood(a.now()))
		leds["effect"], leds["color"], leds["speed"], leds["seconds"] = "blink", "#ff0000", 4, 0.5
		r.conn.command("leds", leds)
		a.afterGame(r, g, 600*time.Millisecond, func() {
			if cg.next < len(cg.seq) {
				a.colorLEDs(r, cg.seq[cg.next])
			}
		})
		return
	}
	r.conn.command("sprite", map[string]any{"id": fmt.Sprintf("c:s%d", cg.next), "opacity": 0.2}) // done
	cg.next++
	a.play(r, sound.Chirp, false)
	if cg.next < len(cg.seq) {
		a.colorLEDs(r, cg.seq[cg.next])
		return
	}
	cg.total += time.Since(cg.started) // the round is done
	g.hits++
	r.pet.Caught(a.now())
	leds := idleSides(r.pet, r.pet.Mood(a.now()))
	leds["effect"], leds["color"], leds["speed"], leds["seconds"] = "blink", "#00ff40", 3, 1
	r.conn.command("leds", leds)
	a.publishState(r)
	round := g.round
	a.afterGame(r, g, a.timing.gap, func() {
		if g.round == round {
			a.colorRound(r, g)
		}
	})
}

// endColors: the face says how long it took (or the new record).
func (a *App) endColors(r *robot, g *game) {
	now := a.now()
	cg := g.colors
	r.game = nil
	a.restoreHead(r)
	a.clearSprites(r)
	re := r.pet.FinishColors(now, g.hits, cg.total)
	a.log.Info("color game over", "robot", r.id, "rounds", g.hits, "seconds", cg.total.Seconds(), "wrong", cg.wrong, "record", re.Record)
	a.dirty = true
	a.begin(r, now, 6*time.Second)
	a.emotion(r, "happy")
	a.play(r, sound.Tada, false)
	leds := idleSides(r.pet, r.pet.Mood(now))
	leds["effect"], leds["seconds"], leds["speed"] = "rainbow", 3, 2
	r.conn.command("leds", leds)
	key := "color_done"
	if re.Record {
		key = "color_record"
	}
	a.say(r, key, secondsText(r.pet.Settings.Lang, cg.total), 4)
	r.conn.command("nod", nil)
	a.publishReaction(r, re)
	a.publishState(r)
}
