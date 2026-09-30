package app

import (
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
	"github.com/mj41/stackchan-pet/internal/robotpic"
	"github.com/mj41/stackchan-pet/internal/sound"
	"github.com/mj41/stackchan-server/wire"
)

// The engine turns robot events into pet actions, and the pet's mood and
// reactions into robot commands: face, speech bubble, stickers, LEDs, head
// gestures and short sounds. Everything here runs with a.mu held.

var moodEmotion = map[pet.Mood]string{
	pet.Happy: "happy", pet.OK: "neutral", pet.Hungry: "sad", pet.Bored: "doubt",
	pet.Tired: "sleepy", pet.Napping: "sleepy", pet.Sleeping: "sleepy",
}

// Idle LED colours per mood, dim: the LEDs sit right next to the kid's eyes.
var moodColor = map[pet.Mood]string{
	pet.Happy: "#402a00", pet.OK: "#161616", pet.Hungry: "#301000", pet.Bored: "#001a40",
	pet.Tired: "#1a0030", pet.Napping: "#0a0018",
}

const (
	nightLightColor = "#180600"
	reactionTime    = 4 * time.Second  // how long a reaction shows before the mood comes back
	nagEvery        = 30 * time.Minute // "I'm hungry!" at most this often
	helloEvery      = 20 * time.Minute // greeting someone who comes close
)

// moodLEDs is the "leds" command for a mood.
func moodLEDs(p *pet.Pet, mood pet.Mood) map[string]any {
	switch mood {
	case pet.Sleeping:
		if p.Settings.NightLight {
			return map[string]any{"left": nightLightColor, "right": nightLightColor}
		}
		return map[string]any{"effect": "off"}
	case pet.Hungry: // slow orange breathing: "feed me"
		c := moodColor[mood]
		return map[string]any{"left": c, "right": c, "effect": "breathe", "color": "#ff6000", "speed": 0.3}
	}
	c := moodColor[mood]
	return map[string]any{"left": c, "right": c}
}

// idleSides are the mood colours to come back to after a timed LED effect.
func idleSides(p *pet.Pet, mood pet.Mood) map[string]any {
	c := moodColor[mood]
	if c == "" {
		c = "#000000"
	}
	return map[string]any{"left": c, "right": c}
}

// express shows the current mood: face, emotion, LEDs, screen on by day.
func (a *App) express(r *robot, now time.Time) {
	c := r.conn
	if c == nil {
		return
	}
	mood := r.pet.Mood(now)
	r.shownMood = mood
	r.busyUntil = time.Time{}
	if r.pictureOn {
		c.command("face", nil)
		r.pictureOn = false
	}
	if r.screenOff && mood != pet.Sleeping {
		c.command("screensaver", map[string]any{"on": false})
		r.screenOff = false
	}
	c.command("emotion", map[string]any{"name": moodEmotion[mood]})
	c.command("leds", moodLEDs(r.pet, mood))
}

// later runs fn after d unless another reaction started meanwhile (or the robot left).
func (a *App) later(r *robot, d time.Duration, fn func()) {
	gen := r.gen
	time.AfterFunc(d, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.gen == gen && r.conn != nil {
			fn()
		}
	})
}

// begin starts a reaction: the mood waits until it is over.
func (a *App) begin(r *robot, now time.Time, d time.Duration) {
	r.gen++
	r.busyUntil = now.Add(d)
	a.later(r, d, func() { a.express(r, a.now()) })
}

func (a *App) say(r *robot, key, arg string, seconds float64) {
	if t := text(r.pet.Settings.Lang, key, arg); t != "" {
		r.conn.command("say", map[string]any{"text": t, "seconds": seconds})
	}
}

// play sends a sound when sounds are on; not at night unless atNight (the lullaby).
func (a *App) play(r *robot, name string, atNight bool) {
	s := r.pet.Settings
	if !s.Sounds || (!atNight && s.PhaseAt(a.now()) == pet.Night) {
		return
	}
	if pcm := sound.PCM(name); pcm != nil {
		r.conn.binary(wire.BinSpeakerPCM, sound.Message(pcm))
	}
}

func (a *App) emotion(r *robot, name string) {
	r.conn.command("emotion", map[string]any{"name": name})
}

func (a *App) sleepScreen(r *robot) {
	if r.pet.Settings.ScreenOffAtNight {
		r.conn.command("screensaver", map[string]any{"on": true})
		r.screenOff = true
	}
}

// react shows what an action did.
func (a *App) react(r *robot, re pet.Reaction, now time.Time) {
	c := r.conn
	if c == nil {
		return
	}
	p := r.pet
	lang := p.Settings.Lang
	switch re.Kind {
	case pet.KindAsleep: // a touch at night: one sleepy answer, then dark again
		if now.Sub(r.lastAsleep) < 20*time.Second {
			return
		}
		r.lastAsleep = now
		r.gen++
		r.busyUntil = now.Add(12 * time.Second)
		a.emotion(r, "sleepy")
		a.say(r, "asleep", "", 4)
		a.later(r, 12*time.Second, func() { a.sleepScreen(r) })
		return
	case pet.KindEat:
		a.begin(r, now, reactionTime)
		c.binary(wire.BinShowJPEG, robotpic.Food(re.Food))
		r.pictureOn = true
		a.play(r, sound.Munch, false)
		a.later(r, 2500*time.Millisecond, func() {
			c.command("face", nil)
			r.pictureOn = false
			a.emotion(r, "happy")
			a.say(r, "eat", foodNames[lang][re.Food], 3)
			c.command("nod", nil)
		})
	case pet.KindFull:
		a.begin(r, now, reactionTime)
		a.emotion(r, "doubt")
		a.say(r, "full", "", 3)
		a.play(r, sound.No, false)
		c.command("shake", nil)
	case pet.KindCuddle:
		if !re.Changed { // stroking on: already reacting
			return
		}
		a.begin(r, now, reactionTime)
		a.emotion(r, "happy")
		c.command("sticker", map[string]any{"name": "heart", "seconds": 2})
		a.say(r, "cuddle", "", 3)
		a.play(r, sound.Chirp, false)
	case pet.KindPlay:
		a.begin(r, now, 5*time.Second)
		a.emotion(r, "happy")
		leds := idleSides(p, p.Mood(now))
		leds["effect"], leds["seconds"], leds["speed"] = "rainbow", 4, 2
		c.command("leds", leds)
		a.say(r, "play", "", 3)
		a.play(r, sound.Tada, false)
		// a little dance: look left, right, back
		c.command("look", map[string]any{"yaw": -25, "pitch": 10})
		a.later(r, 700*time.Millisecond, func() { c.command("look", map[string]any{"yaw": 25, "pitch": 10}) })
		a.later(r, 1400*time.Millisecond, func() { c.command("home", nil) })
	case pet.KindTooTired, pet.KindLimit:
		a.begin(r, now, reactionTime)
		a.emotion(r, "sleepy")
		a.say(r, re.Kind, "", 4)
		a.play(r, sound.Yawn, false)
	case pet.KindNap:
		if !re.Changed {
			return
		}
		a.begin(r, now, reactionTime)
		a.emotion(r, "sleepy")
		a.say(r, "nap", "", 3)
		a.play(r, sound.Yawn, false)
	case pet.KindNotTired:
		a.begin(r, now, reactionTime)
		a.emotion(r, "doubt")
		a.say(r, "not_tired", "", 3)
		a.play(r, sound.No, false)
	case pet.KindWake:
		if !re.Changed {
			return
		}
		a.begin(r, now, reactionTime)
		a.emotion(r, "happy")
		a.say(r, "wake", "", 3)
		a.play(r, sound.Chirp, false)
	case pet.KindShake:
		a.begin(r, now, 3*time.Second)
		c.command("sticker", map[string]any{"name": "dizzy", "seconds": 2})
		a.say(r, "shake", "", 2)
		a.play(r, sound.Whee, false)
	}
}

// showNeeds covers the face with the needs picture for a few seconds.
func (a *App) showNeeds(r *robot, now time.Time) {
	if r.conn == nil {
		return
	}
	s := r.pet.Stats
	a.begin(r, now, 5*time.Second)
	r.conn.binary(wire.BinShowJPEG, robotpic.Needs(s.Food, s.Fun, s.Energy))
	r.pictureOn = true
}

// robotEvent handles something that happened on the robot.
func (a *App) robotEvent(id string, ev wire.RobotEventBody) {
	a.mu.Lock()
	defer a.mu.Unlock()
	r := a.robots[id]
	if r == nil {
		return
	}
	now := a.now()
	p := r.pet
	r.lastSeen = now
	var re pet.Reaction
	switch ev.Name {
	case "head_press", "head_swipe_forward", "head_swipe_backward":
		re = p.Cuddle(now)
	case "nfc_tag": // a food card
		uid, _ := ev.Data["uid"].(string)
		food, ok := p.Settings.Foods[uid]
		if !ok {
			food = "apple" // works right away; the parent can pick the food later
			if uid != "" {
				r.unknownTags[uid] = now
				trimTags(r.unknownTags)
			}
		}
		re = p.Feed(now, food)
		a.log.Info("food card", "robot", id, "uid", uid, "food", food, "reaction", re.Kind)
	case "screen_tap":
		switch {
		case p.Settings.PhaseAt(now) == pet.Night:
			re = pet.Reaction{Kind: pet.KindAsleep}
		case p.Napping(now):
			re = p.Wake(now)
		case r.pictureOn: // tap again: back to the face
			a.express(r, now)
			return
		default:
			p.Advance(now)
			a.showNeeds(r, now)
			a.publishState(r)
			return
		}
	case "shake":
		re = p.Shake(now)
	case "proximity_near":
		a.hello(r, now)
		return
	default:
		return
	}
	a.dirty = true
	a.react(r, re, now)
	a.publishReaction(r, re)
	a.publishState(r)
}

// trimTags keeps the 20 most recently seen unknown tags.
func trimTags(tags map[string]time.Time) {
	for len(tags) > 20 {
		oldest := ""
		for uid, t := range tags {
			if oldest == "" || t.Before(tags[oldest]) {
				oldest = uid
			}
		}
		delete(tags, oldest)
	}
}

// hello greets someone who came close, now and then, by day.
func (a *App) hello(r *robot, now time.Time) {
	p := r.pet
	if r.conn == nil || p.Settings.PhaseAt(now) == pet.Night || p.Napping(now) ||
		now.Sub(r.lastHello) < helloEvery || now.Before(r.busyUntil) {
		return
	}
	r.lastHello = now
	a.begin(r, now, 3*time.Second)
	a.emotion(r, "happy")
	a.say(r, "hello", "", 2)
	a.play(r, sound.Hello, false)
}

// robotOnline sets the robot up after it connects.
func (a *App) robotOnline(r *robot) {
	now := a.now()
	p := r.pet
	p.Advance(now)
	r.phase = p.Settings.PhaseAt(now)
	r.pictureOn, r.screenOff = false, false
	r.gen++
	if p.Settings.Sounds {
		r.conn.command("volume", map[string]any{"value": p.Settings.Volume})
	}
	a.express(r, now)
	if r.phase == pet.Night && a.viewers(r.id) > 0 { // not while the QR code waits for a scan
		a.later(r, 20*time.Second, func() { a.sleepScreen(r) })
	}
}

// Tick advances every pet and plays the day: morning, bedtime, moods, nags.
func (a *App) Tick() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	for _, r := range a.robots {
		p := r.pet
		p.Advance(now)
		a.dirty = true
		phase := p.Settings.PhaseAt(now)
		prev := r.phase
		r.phase = phase
		if r.conn == nil {
			continue
		}
		switch {
		case prev != "" && prev != pet.Night && phase == pet.Night:
			a.goodnight(r, now)
		case prev == pet.Night && phase != pet.Night:
			a.morning(r, now)
		case phase != pet.Night && now.After(r.busyUntil):
			a.daytime(r, now)
		}
		a.publishState(r)
	}
}

// daytime: bedtime warning, a finished nap, mood changes and nags.
func (a *App) daytime(r *robot, now time.Time) {
	p := r.pet
	mood := p.Mood(now)
	today := now.Format(time.DateOnly)
	if m := p.Settings.MinutesToBed(now); m > 0 && m <= 10 && r.bedWarned != today {
		r.bedWarned = today
		a.begin(r, now, 5*time.Second)
		a.emotion(r, "sleepy")
		a.say(r, "bedtime", "", 4)
		a.play(r, sound.Yawn, false)
		return
	}
	if r.shownMood == pet.Napping && mood != pet.Napping {
		a.react(r, pet.Reaction{Kind: pet.KindWake, Changed: true}, now)
		return
	}
	if r.phase == pet.Awake && now.Sub(r.lastNag) >= nagEvery {
		key := map[pet.Mood]string{pet.Hungry: "hungry", pet.Bored: "bored", pet.Tired: "tired"}[mood]
		if key != "" {
			r.lastNag = now
			a.begin(r, now, reactionTime)
			a.emotion(r, moodEmotion[mood])
			if mood == pet.Hungry {
				r.conn.command("sticker", map[string]any{"name": "sweat", "seconds": 2})
			}
			a.say(r, key, "", 4)
			a.play(r, map[pet.Mood]string{pet.Hungry: sound.No, pet.Bored: sound.Chirp, pet.Tired: sound.Yawn}[mood], false)
			return
		}
	}
	if mood != r.shownMood {
		a.express(r, now)
	}
}

// goodnight: lullaby, sleepy face, night light, then the screen goes dark.
func (a *App) goodnight(r *robot, now time.Time) {
	p := r.pet
	p.Note(now, "night", "")
	r.gen++
	r.busyUntil = now.Add(15 * time.Second)
	r.shownMood = pet.Sleeping
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "sleepy")
	a.say(r, "goodnight", "", 6)
	a.play(r, sound.Lullaby, true)
	r.conn.command("leds", moodLEDs(p, pet.Sleeping))
	a.later(r, 10*time.Second, func() { a.sleepScreen(r) })
}

// morning: screen on, a greeting and a sunrise on the LEDs.
func (a *App) morning(r *robot, now time.Time) {
	p := r.pet
	p.Note(now, "morning", "")
	a.begin(r, now, 7*time.Second)
	r.conn.command("screensaver", map[string]any{"on": false})
	r.screenOff = false
	a.emotion(r, "happy")
	a.say(r, "morning", "", 5)
	a.play(r, sound.Morning, false)
	leds := idleSides(p, p.Mood(now))
	leds["effect"], leds["color"], leds["speed"], leds["seconds"] = "breathe", "#ffb000", 0.5, 6
	r.conn.command("leds", leds)
}
