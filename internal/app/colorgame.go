package app

import (
	"math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
	"github.com/mj41/stackchan-pet/internal/robotpic"
	"github.com/mj41/stackchan-pet/internal/sound"
)

// The color game: the screen shows six color buttons, three by two, in a new order
// every round; the robot's LED strips show colors, and the kid presses them in the
// level's order. Each level starts with a picture of the robot from above, its
// strips numbered in that order (robotpic.Order):
//
//	level 1  the left strip, then the right one
//	level 2  the right strip, then the left one
//	level 3  left near the screen, left far, right near, right far
//	level 4  right far, right near, left far, left near
//	level 5  the four halves in a random order (not level 3's or 4's)
//
// The same color twice in a row is a double tap. A part goes dark once pressed. A
// wrong button only buzzes, the clock runs on; a level not done in colorRoundMax
// moves on and counts with that time. At the end the robot says the time over all
// levels; the best time is kept. It is a game like catch (r.game, with colors
// set). Everything here runs with a.mu held.

// colorRoundMax ends a level the kid does not finish; it counts with this time.
const colorRoundMax = 20 * time.Second

type colorGame struct {
	parts   int      // 2 (left, right) or 4 (halves)
	order   []int    // the parts in the order to press
	colors  []string // each part's color
	next    int      // how many are pressed
	started time.Time
	total   time.Duration // the levels so far
	wrong   int
}

// pressNext is the color to press next.
func (cg *colorGame) pressNext() string { return cg.colors[cg.order[cg.next]] }

// The LED strips: 12 single LEDs, left 0-5, right 6-11 (wire "leds" pixels). The
// halves near the kid (the screen) and far from it: the left strip starts at the
// screen, the right one runs the other way (seen on the robot).
var (
	ledLeft      = []int{0, 1, 2, 3, 4, 5}
	ledRight     = []int{6, 7, 8, 9, 10, 11}
	ledLeftNear  = []int{0, 1, 2}
	ledLeftFar   = []int{3, 4, 5}
	ledRightNear = []int{9, 10, 11}
	ledRightFar  = []int{6, 7, 8}
)

// colorParts are the LEDs of each part: 2 the strips, 4 their halves.
func colorParts(n int) [][]int {
	if n == 2 {
		return [][]int{ledLeft, ledRight}
	}
	return [][]int{ledLeftNear, ledLeftFar, ledRightNear, ledRightFar}
}

// colorLevel is level's parts and order (1-based level).
func colorLevel(level int) (int, []int) {
	switch level {
	case 1:
		return 2, []int{0, 1}
	case 2:
		return 2, []int{1, 0}
	case 3:
		return 4, []int{0, 1, 2, 3}
	case 4:
		return 4, []int{3, 2, 1, 0}
	}
	for {
		order := rand.Perm(4)
		if !slices.Equal(order, []int{0, 1, 2, 3}) && !slices.Equal(order, []int{3, 2, 1, 0}) {
			return 4, order
		}
	}
}

// colorSpots are the button centers on the 320x240 screen, three by two.
var colorSpots = [6][2]int{{56, 62}, {160, 62}, {264, 62}, {56, 178}, {160, 178}, {264, 178}}

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
	a.gameScreen(r, true)
	a.clearSprites(r)
	a.emotion(r, "happy")
	a.sprite(r, map[string]any{"id": "c:bg", "asset": assetDir + "menu-bg.png", "x": 160, "y": 120, "z": 20})
	a.say(r, "color_game", "", 2)
	a.play(r, sound.Hello, false)
	a.publishState(r)
	a.afterGame(r, g, a.timing.intro, func() { a.colorRound(r, g) })
}

// colorRound starts the next level: its order picture first, then the buttons and the colors.
func (a *App) colorRound(r *robot, g *game) {
	if g.round >= pet.GameRounds {
		a.endColors(r, g)
		return
	}
	g.round++
	cg := g.colors
	cg.parts, cg.order = colorLevel(g.round)
	cg.colors, cg.next = make([]string, cg.parts), 0
	for i := range cg.colors {
		cg.colors[i] = robotpic.Colors[rand.IntN(len(robotpic.Colors))]
	}
	r.conn.command("leds", map[string]any{"pixels": slices.Repeat([]string{"#000000"}, 12)})
	for _, id := range append([]string{"c:bg"}, colorIDs()...) { // the picture is under the sprites
		r.conn.command("sprite", map[string]any{"id": id, "hidden": true})
	}
	a.showPicture(r, robotpic.Order(cg.parts, cg.order))
	r.busyUntil = a.now().Add(time.Minute)
	a.publishState(r)
	round := g.round
	a.afterGame(r, g, 2*a.timing.intro, func() {
		if g.round != round {
			return
		}
		r.conn.command("face", nil)
		r.pictureOn = false
		r.conn.command("sprite", map[string]any{"id": "c:bg", "hidden": false})
		for i, spot := range rand.Perm(len(colorSpots)) {
			c := robotpic.Colors[i]
			a.sprite(r, map[string]any{"id": "c:" + c, "asset": assetDir + "color-" + c + ".png",
				"x": colorSpots[spot][0], "y": colorSpots[spot][1], "z": 21, "tap": true, "hidden": false})
		}
		a.colorLEDs(r, cg)
		cg.started = time.Now()
		a.afterGame(r, g, colorRoundMax, func() {
			if g.round == round && cg.next < cg.parts { // not finished: on to the next level
				cg.total += colorRoundMax
				a.colorRound(r, g)
			}
		})
	})
}

// colorIDs are the buttons' sprite ids.
func colorIDs() []string {
	ids := make([]string, len(robotpic.Colors))
	for i, c := range robotpic.Colors {
		ids[i] = "c:" + c
	}
	return ids
}

// colorLEDs shows on the LED strips the colors still to press; the pressed ones go dark.
func (a *App) colorLEDs(r *robot, cg *colorGame) {
	pixels := slices.Repeat([]string{"#000000"}, 12)
	parts := colorParts(cg.parts)
	for i, part := range parts {
		if slices.Contains(cg.order[:cg.next], i) {
			continue // pressed
		}
		for _, p := range part {
			pixels[p] = robotpic.ColorLED[cg.colors[i]]
		}
	}
	r.conn.command("leds", map[string]any{"pixels": pixels})
}

// colorTap is a tap on a button (sprite id "c:<color>") during the color game.
func (a *App) colorTap(r *robot, g *game, sprite string) {
	cg := g.colors
	c, ok := strings.CutPrefix(sprite, "c:")
	if !ok || !slices.Contains(robotpic.Colors, c) || cg.started.IsZero() || cg.next >= cg.parts {
		return
	}
	if c != cg.pressNext() {
		cg.wrong++
		a.play(r, sound.No, false)
		return
	}
	cg.next++
	a.play(r, sound.Chirp, false)
	a.colorLEDs(r, cg)
	if cg.next < cg.parts {
		return
	}
	cg.total += time.Since(cg.started) // the level is done
	cg.started = time.Time{}           // no presses until the next level's buttons
	g.hits++
	r.pet.Caught(a.now())
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
	a.gameScreen(r, false)
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
