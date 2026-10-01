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
// every round; the robot's LED strips show the colors to press, in reading order:
//
//	1 color   the left strip (the right one stays dark)
//	2 colors  the left strip, then the right one
//	3 colors  the left strip's half near the kid (the screen), its far half, then the right near half
//	4 colors  left near, left far, right near, right far
//
// The same color twice in a row is a double tap. A part goes dark once its color
// is pressed. Rounds: 1, 2, 2 the same (a double tap), 3, 4 colors. A wrong
// button only buzzes, the clock runs on; a round not done in colorRoundMax moves
// on and counts with that time. At the end the robot says the time over all
// rounds; the best time is kept. It is a game like catch (r.game, with colors
// set). Everything here runs with a.mu held.

// colorRoundMax ends a round the kid does not finish; it counts with this time.
const colorRoundMax = 20 * time.Second

type colorGame struct {
	seq     []string // the colors to press this round, in order
	next    int      // the next one in seq
	started time.Time
	total   time.Duration // the rounds so far
	wrong   int
}

// The LED strips: 12 single LEDs, left 0-5, right 6-11 (wire "leds" pixels). The
// halves near the kid (the screen) and far from it, for 3 and 4 colors: the left
// strip starts at the screen, the right one runs the other way (seen on the robot).
var (
	ledLeft      = []int{0, 1, 2, 3, 4, 5}
	ledRight     = []int{6, 7, 8, 9, 10, 11}
	ledLeftNear  = []int{0, 1, 2}
	ledLeftFar   = []int{3, 4, 5}
	ledRightNear = []int{9, 10, 11}
	ledRightFar  = []int{6, 7, 8}
)

// colorParts are the LEDs of each color to press, for n colors.
func colorParts(n int) [][]int {
	switch n {
	case 1:
		return [][]int{ledLeft}
	case 2:
		return [][]int{ledLeft, ledRight}
	case 3:
		return [][]int{ledLeftNear, ledLeftFar, ledRightNear}
	}
	return [][]int{ledLeftNear, ledLeftFar, ledRightNear, ledRightFar}
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
	a.clearSprites(r)
	a.emotion(r, "happy")
	a.sprite(r, map[string]any{"id": "c:bg", "asset": assetDir + "menu-bg.png", "x": 160, "y": 120, "z": 20})
	a.say(r, "color_game", "", 2)
	a.play(r, sound.Hello, false)
	a.publishState(r)
	a.afterGame(r, g, a.timing.intro, func() { a.colorRound(r, g) })
}

// colorRound shuffles the buttons and shows the next colors on the LEDs.
func (a *App) colorRound(r *robot, g *game) {
	if g.round >= pet.GameRounds {
		a.endColors(r, g)
		return
	}
	g.round++
	cg := g.colors
	for i, spot := range rand.Perm(len(colorSpots)) {
		c := robotpic.Colors[i]
		a.sprite(r, map[string]any{"id": "c:" + c, "asset": assetDir + "color-" + c + ".png",
			"x": colorSpots[spot][0], "y": colorSpots[spot][1], "z": 21, "tap": true})
	}
	pick := func() string { return robotpic.Colors[rand.IntN(len(robotpic.Colors))] }
	switch g.round {
	case 1:
		cg.seq = []string{pick()}
	case 2: // two different colors
		p := rand.Perm(len(robotpic.Colors))
		cg.seq = []string{robotpic.Colors[p[0]], robotpic.Colors[p[1]]}
	case 3: // the same color twice: a double tap
		c := pick()
		cg.seq = []string{c, c}
	default: // 3, then 4 colors, any
		cg.seq = nil
		for range g.round - 1 {
			cg.seq = append(cg.seq, pick())
		}
	}
	cg.next = 0
	a.colorLEDs(r, cg)
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

// colorLEDs shows on the LED strips the colors still to press; the pressed ones go dark.
func (a *App) colorLEDs(r *robot, cg *colorGame) {
	pixels := make([]string, 12)
	for i := range pixels {
		pixels[i] = "#000000"
	}
	for i, part := range colorParts(len(cg.seq)) {
		if i < cg.next {
			continue
		}
		for _, p := range part {
			pixels[p] = robotpic.ColorLED[cg.seq[i]]
		}
	}
	r.conn.command("leds", map[string]any{"pixels": pixels})
}

// colorTap is a tap on a button (sprite id "c:<color>") during the color game.
func (a *App) colorTap(r *robot, g *game, sprite string) {
	cg := g.colors
	c, ok := strings.CutPrefix(sprite, "c:")
	if !ok || !slices.Contains(robotpic.Colors, c) || cg.next >= len(cg.seq) {
		return
	}
	if c != cg.seq[cg.next] {
		cg.wrong++
		a.play(r, sound.No, false)
		return
	}
	cg.next++
	a.play(r, sound.Chirp, false)
	a.colorLEDs(r, cg)
	if cg.next < len(cg.seq) {
		return
	}
	cg.total += time.Since(cg.started) // the round is done
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
