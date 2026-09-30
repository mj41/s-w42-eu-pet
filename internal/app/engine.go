package app

import (
	"math/rand/v2"
	"slices"
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
	cuddleWait = 800 * time.Millisecond // a head touch waits this long: a food card may follow
	cardQuiet  = 2 * time.Second        // head touches this soon after a card are the card

	hardPressZone   = 3 // all three head zones at full (0-3): the whole palm; one finger only dreams
	dreamEvery      = 8 * time.Second
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
	a.clearSprites(r)
	if r.pictureOn {
		c.command("face", nil)
		r.pictureOn = false
	}
	if r.screenOff && mood != pet.Sleeping {
		c.command("screensaver", map[string]any{"on": false})
		r.screenOff = false
	}
	c.command("emotion", map[string]any{"name": moodEmotion[mood]})
	a.drawnFace(r, moodFace[mood])
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
	a.sayText(r, text(r.pet.Settings.Lang, key, arg), seconds)
}

func (a *App) sayText(r *robot, t string, seconds float64) {
	if t != "" {
		a.hideFace(r) // the bubble belongs to the robot's own face
		r.conn.command("say", map[string]any{"text": t, "seconds": seconds})
	}
}

// play sends a sound when sounds are on; not at night unless atNight (the lullaby).
func (a *App) play(r *robot, name string, atNight bool) {
	s := r.pet.Settings
	if !s.Sounds || (!atNight && s.PhaseAt(a.now()) == pet.Night) {
		return
	}
	if asset := soundAsset(name); r.files[asset] && slices.Contains(r.commands, "play") {
		r.conn.command("play", map[string]any{"asset": asset}) // stored on the robot: nothing to send
		return
	}
	if pcm := sound.PCM(name); pcm != nil {
		r.conn.binary(wire.BinSpeakerPCM, sound.Message(pcm))
	}
}

func (a *App) emotion(r *robot, name string) {
	r.conn.command("emotion", map[string]any{"name": name})
	if !r.faceHidden {
		a.drawnFace(r, emotionFace[name])
	}
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
	case pet.KindAsleep: // a touch while asleep: mostly a sleepy "Zzz", sometimes a dream
		if rand.Float64() < a.timing.dreamShare {
			a.dream(r, now)
		} else {
			a.snore(r, now)
		}
		return
	case pet.KindEat:
		a.begin(r, now, reactionTime)
		if asset := assetDir + re.Food + ".png"; r.canSprite(asset) {
			a.eatSprite(r, asset, foodText(lang, re.Food))
			break
		}
		a.showPicture(r, robotpic.Food(re.Food))
		a.play(r, sound.Munch, false)
		a.later(r, 2500*time.Millisecond, func() {
			c.command("face", nil)
			r.pictureOn = false
			a.emotion(r, "happy")
			a.sayText(r, foodText(lang, re.Food), 3)
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
	case pet.KindGame: // startGame shows it
		return
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
	a.showPicture(r, robotpic.Needs(s.Food, s.Fun, s.Energy))
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
	if a.gameEvent(r, ev, now) {
		return
	}
	night := p.Phase(now) == pet.Night
	asleep := night || p.Napping(now) // the night, or a daytime nap
	var re pet.Reaction
	switch ev.Name {
	case "screen_long_press": // hold a finger on the screen: a game of catch
		re = a.playAction(r, now)
	case "head_press":
		if !asleep {
			a.cuddleSoon(r, now)
			return
		}
		// Asleep, a light touch (one finger) shows a dream; the whole palm (3,3,3) wakes the pet.
		zones := [3]float64{}
		for i, k := range []string{"z0", "z1", "z2"} {
			zones[i], _ = ev.Data[k].(float64)
		}
		a.log.Info("head press while asleep", "robot", id, "zones", zones, "night", night) // to tune "hard"
		switch {
		case min(zones[0], zones[1], zones[2]) < hardPressZone:
			re = pet.Reaction{Kind: pet.KindAsleep}
		case night:
			a.nightWake(r, now)
			return
		default:
			re = p.Wake(now)
		}
	case "head_swipe_forward", "head_swipe_backward":
		if !asleep {
			a.cuddleSoon(r, now)
			return
		}
		re = pet.Reaction{Kind: pet.KindAsleep} // a stroke while asleep: a dream
	case "nfc_tag": // a food card
		r.lastCard = now
		r.cuddleGen++ // the card touched the head on its way: not a cuddle
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
		case night:
			re = pet.Reaction{Kind: pet.KindAsleep}
		case p.Napping(now): // a tap does not wake a napping pet: a dream (the palm on the head does)
			re = pet.Reaction{Kind: pet.KindAsleep}
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
	case "assets": // the robot's file store
		list, _ := ev.Data["list"].(string)
		free, _ := ev.Data["free"].(float64)
		total, _ := ev.Data["total"].(float64)
		a.log.Info("robot files", "robot", id, "free_kb", int(free/1024), "total_kb", int(total/1024))
		a.syncAssets(r, list)
		return
	case "asset_saved":
		name, _ := ev.Data["name"].(string)
		crc, _ := ev.Data["crc"].(float64)
		a.assetSaved(r, name, uint32(crc))
		return
	case "asset_error":
		a.log.Warn("robot file upload failed", "robot", id, "name", ev.Data["name"], "reason", ev.Data["reason"])
		return
	case "sound_error":
		a.log.Warn("robot could not play a sound", "robot", id, "asset", ev.Data["asset"], "reason", ev.Data["reason"])
		return
	case "sound_done":
		return
	case "sprite_error":
		a.log.Warn("robot could not show a sprite", "robot", id, "id", ev.Data["id"], "reason", ev.Data["reason"])
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
	r.phase = p.Phase(now)
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
		phase := p.Phase(now) // a pet woken at night counts as awake until it falls asleep again
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
	wokenAtNight := p.Settings.PhaseAt(now) == pet.Night // quiet: no warnings, no nags
	if m := p.Settings.MinutesToBed(now); !wokenAtNight && m > 0 && m <= 10 && r.bedWarned != today {
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
	if r.phase == pet.Awake && !wokenAtNight && now.Sub(r.lastNag) >= nagEvery {
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
	a.stopGame(r)
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

// dream shows what the sleeping pet dreams of: a light touch at night or during
// a nap. No sound; the sleepy face comes back (and at night the screen goes dark).
func (a *App) dream(r *robot, now time.Time) {
	if r.conn == nil || now.Sub(r.lastAsleep) < dreamEvery {
		return
	}
	r.lastAsleep = now
	r.gen++
	r.busyUntil = now.Add(10 * time.Second)
	item := robotpic.Dreams[rand.IntN(len(robotpic.Dreams))]
	if cloud, pic := assetDir+"cloud.png", assetDir+item+".png"; r.canSprite(cloud) && r.canSprite(pic) {
		// A dream cloud next to the sleeping face, the dreamed thing bobbing in it.
		if r.screenOff {
			r.conn.command("screensaver", map[string]any{"on": false})
			r.screenOff = false
		}
		if r.pictureOn {
			r.conn.command("face", nil)
			r.pictureOn = false
		}
		a.emotion(r, "sleepy")
		a.sprite(r, map[string]any{"id": "cloud", "asset": cloud, "x": 232, "y": 72, "scale": 0.75, "opacity": 0.95, "z": 1})
		a.sprite(r, map[string]any{"id": "dream", "asset": pic, "x": 239, "y": 70, "scale": 0.4, "z": 2})
		for i, y := range []int{60, 72, 60, 72} {
			a.later(r, time.Duration(800+i*1200)*time.Millisecond, func() { a.sprite(r, map[string]any{"id": "dream", "y": y, "ms": 1100}) })
		}
	} else {
		a.showPicture(r, robotpic.Dream(item))
	}
	a.later(r, 6*time.Second, func() { a.express(r, a.now()) })
	if r.pet.Phase(now) == pet.Night { // a daytime nap keeps the screen on
		a.later(r, 9*time.Second, func() { a.sleepScreen(r) })
	}
}

// nightWake: a hard press at night wakes the pet for a few minutes (quietly);
// when the time is up the Tick sees night again and plays the lullaby.
func (a *App) nightWake(r *robot, now time.Time) {
	re := r.pet.WakeAtNight(now)
	a.publishReaction(r, re)
	if re.Kind != pet.KindNightWake {
		a.dream(r, now) // waking at night is off: dream on
		return
	}
	if !re.Changed || r.conn == nil {
		return
	}
	a.dirty = true
	r.phase = pet.Awake // woken, not morning: no morning greeting
	a.begin(r, now, reactionTime)
	r.conn.command("screensaver", map[string]any{"on": false})
	r.screenOff = false
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "sleepy")
	a.say(r, "night_wake", "", 4)
	a.publishState(r)
}

// snore: the sleepy face murmurs "Zzz..." and sleeps on (a touch while asleep).
func (a *App) snore(r *robot, now time.Time) {
	if r.conn == nil || now.Sub(r.lastAsleep) < 3*time.Second {
		return
	}
	r.lastAsleep = now
	r.gen++
	r.busyUntil = now.Add(4 * time.Second)
	if r.screenOff {
		r.conn.command("screensaver", map[string]any{"on": false}) // the touch woke the screen anyway
		r.screenOff = false
	}
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "sleepy")
	r.conn.command("say", map[string]any{"text": "Zzz...", "seconds": 2.5})
	a.later(r, 3*time.Second, func() { a.express(r, a.now()) })
	if r.pet.Phase(now) == pet.Night {
		a.later(r, 5*time.Second, func() { a.sleepScreen(r) })
	}
}

// eatSprite: the food drops in from the top and glides to the mouth over the face,
// then shrinks bite by bite (munch, nod) and the pet says what it ate.
func (a *App) eatSprite(r *robot, asset, line string) {
	c := r.conn
	a.emotion(r, "happy")
	a.drawnFace(r, "yum")
	a.sprite(r, map[string]any{"id": "food", "asset": asset, "x": 160, "y": -60, "scale": 0.8, "z": 1})
	a.sprite(r, map[string]any{"id": "food", "x": 160, "y": 170, "ms": 700})
	a.later(r, 750*time.Millisecond, func() {
		a.play(r, sound.Munch, false)
		c.command("nod", nil)
		a.sprite(r, map[string]any{"id": "food", "scale": 0.55})
	})
	a.later(r, 1150*time.Millisecond, func() { a.sprite(r, map[string]any{"id": "food", "scale": 0.3}) })
	a.later(r, 1500*time.Millisecond, func() {
		c.command("sprite_hide", map[string]any{"id": "food"})
		a.sayText(r, line, 3)
	})
}

// cuddleSoon turns a head touch into a cuddle after a short wait: holding a food card
// to the robot touches its head too, and the card (read a moment later) must win.
func (a *App) cuddleSoon(r *robot, now time.Time) {
	if now.Sub(r.lastCard) < cardQuiet {
		return
	}
	r.cuddleGen++
	gen := r.cuddleGen
	time.AfterFunc(cuddleWait, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		now := a.now()
		if r.cuddleGen != gen || r.conn == nil || r.game != nil || now.Sub(r.lastCard) < cardQuiet {
			return
		}
		re := r.pet.Cuddle(now)
		a.dirty = true
		a.react(r, re, now)
		a.publishReaction(r, re)
		a.publishState(r)
	})
}
