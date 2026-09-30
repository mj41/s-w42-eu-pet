// Package pet is the Tamagotchi model: three needs (food, fun, energy), the
// family's daily schedule and the kid's actions. It does no I/O: the caller
// passes the time and turns reactions into robot commands.
//
// The pet is kind: it never dies or gets ill. Neglected it only gets sad, and
// its needs pause while the kid sleeps or is at school.
package pet

import (
	"math"
	"time"
)

// Stats are the needs, 0 (empty) to 100 (full).
type Stats struct {
	Food   float64 `json:"food"`
	Fun    float64 `json:"fun"`
	Energy float64 `json:"energy"`
}

// Phase is the part of the day the schedule says it is.
type Phase string

const (
	Awake  Phase = "awake"
	Night  Phase = "night"
	School Phase = "school" // needs paused; the kid can still play (holidays)
)

// Mood is what the pet shows, from the phase and its needs.
type Mood string

const (
	Happy    Mood = "happy"
	OK       Mood = "ok"
	Hungry   Mood = "hungry"
	Bored    Mood = "bored"
	Tired    Mood = "tired"
	Napping  Mood = "napping"
	Sleeping Mood = "sleeping" // night
)

// Entry is one line of the parent's activity log.
type Entry struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`             // feed, cuddle, play, nap, morning, night, ...
	Detail string    `json:"detail,omitempty"` // e.g. the food
}

const keepLog = 300

// Pet is one robot's pet. Stats are current as of Updated; call Advance first.
type Pet struct {
	Settings Settings  `json:"settings"`
	Stats    Stats     `json:"stats"`
	Born     time.Time `json:"born"`
	Updated  time.Time `json:"updated"`
	NapUntil time.Time `json:"nap_until,omitzero"`

	PlayDay    string    `json:"play_day"` // the local date PlayMin counts, "2006-01-02"
	PlayMin    float64   `json:"play_min"` // play and cuddle time on PlayDay
	LastPlayAt time.Time `json:"last_play_at,omitzero"`
	LastCuddle time.Time `json:"last_cuddle,omitzero"`

	Log []Entry `json:"log"`
}

// New is a fresh pet: fed and rested, a bit bored.
func New(now time.Time, s Settings) *Pet {
	s.Normalize()
	return &Pet{
		Settings: s,
		Stats:    Stats{Food: 80, Fun: 60, Energy: 90},
		Born:     now,
		Updated:  now,
	}
}

// rates are points per hour.
type rates struct{ food, fun, energy, nightEnergy, napEnergy float64 }

func (s *Settings) rates() rates {
	r := rates{food: 12, fun: 15, energy: 7, nightEnergy: 25, napEnergy: 60}
	f := map[string]float64{"easy": 0.5, "normal": 1, "hard": 1.5}[s.Difficulty]
	if f == 0 {
		f = 1
	}
	r.food *= f
	r.fun *= f
	r.energy *= f
	return r
}

// maxCatchUp limits how much time Advance simulates after a long pause (server down).
const maxCatchUp = 72 * time.Hour

// Advance moves the needs to now, minute by minute, following the schedule.
func (p *Pet) Advance(now time.Time) {
	if p.Updated.IsZero() || !now.After(p.Updated) {
		if p.Updated.IsZero() {
			p.Updated = now
		}
		return
	}
	from := p.Updated
	if now.Sub(from) > maxCatchUp {
		from = now.Add(-maxCatchUp)
	}
	r := p.Settings.rates()
	for t := from; t.Before(now); {
		step := min(time.Minute, now.Sub(t))
		h := step.Hours()
		switch p.Settings.PhaseAt(t) {
		case Night:
			p.Stats.Energy += r.nightEnergy * h
		case School:
			// waiting for the kid
		default:
			if t.Before(p.NapUntil) {
				p.Stats.Energy += r.napEnergy * h
				p.Stats.Food -= r.food * h / 2
			} else {
				p.Stats.Food -= r.food * h
				p.Stats.Fun -= r.fun * h
				p.Stats.Energy -= r.energy * h
			}
		}
		t = t.Add(step)
	}
	p.clamp()
	p.Updated = now
	if !p.NapUntil.IsZero() && !now.Before(p.NapUntil) {
		p.NapUntil = time.Time{}
	}
}

func (p *Pet) clamp() {
	c := func(v float64) float64 { return math.Round(max(0, min(100, v))*100) / 100 }
	p.Stats.Food, p.Stats.Fun, p.Stats.Energy = c(p.Stats.Food), c(p.Stats.Fun), c(p.Stats.Energy)
}

// Napping reports whether the pet takes a daytime nap at now.
func (p *Pet) Napping(now time.Time) bool { return now.Before(p.NapUntil) }

// Mood at now (after Advance).
func (p *Pet) Mood(now time.Time) Mood {
	switch {
	case p.Settings.PhaseAt(now) == Night:
		return Sleeping
	case p.Napping(now):
		return Napping
	case p.Stats.Food < 25:
		return Hungry
	case p.Stats.Energy < 20:
		return Tired
	case p.Stats.Fun < 25:
		return Bored
	case min(p.Stats.Food, p.Stats.Fun, p.Stats.Energy) >= 60:
		return Happy
	}
	return OK
}

// AgeDays counts the nights since the pet was born (local dates).
func (p *Pet) AgeDays(now time.Time) int {
	b := p.Born.In(now.Location())
	d0 := time.Date(b.Year(), b.Month(), b.Day(), 0, 0, 0, 0, now.Location())
	d1 := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return int(math.Round(d1.Sub(d0).Hours() / 24))
}

// PlayLeft is how many minutes of play are left today, or -1 without a limit.
func (p *Pet) PlayLeft(now time.Time) float64 {
	if p.Settings.PlayLimitMin <= 0 {
		return -1
	}
	used := p.PlayMin
	if p.PlayDay != now.Format(time.DateOnly) {
		used = 0
	}
	return max(0, float64(p.Settings.PlayLimitMin)-used)
}

// PlayedToday is the play and cuddle time today in minutes.
func (p *Pet) PlayedToday(now time.Time) float64 {
	if p.PlayDay != now.Format(time.DateOnly) {
		return 0
	}
	return p.PlayMin
}

// countPlay adds the time since the previous interaction, capped: separate
// taps a few seconds apart add up to how long the kid actually played.
func (p *Pet) countPlay(now time.Time) {
	if day := now.Format(time.DateOnly); p.PlayDay != day {
		p.PlayDay, p.PlayMin = day, 0
	}
	d := now.Sub(p.LastPlayAt)
	if p.LastPlayAt.IsZero() || d < 0 || d > time.Minute {
		d = 15 * time.Second
	}
	p.PlayMin += d.Minutes()
	p.LastPlayAt = now
}

func (p *Pet) log(now time.Time, kind, detail string) {
	p.Log = append(p.Log, Entry{At: now, Kind: kind, Detail: detail})
	if len(p.Log) > keepLog {
		p.Log = p.Log[len(p.Log)-keepLog:]
	}
}

// Note adds a line to the log (the caller's events: morning, night, ...).
func (p *Pet) Note(now time.Time, kind, detail string) { p.log(now, kind, detail) }

// Reaction is what an action did; the caller shows it on the robot and the page.
type Reaction struct {
	Kind    string `json:"kind"`           // see the Kind* constants
	Food    string `json:"food,omitempty"` // for KindEat
	Changed bool   `json:"changed"`        // the needs changed
}

const (
	KindEat      = "eat"
	KindFull     = "full"      // not hungry: refuses food
	KindCuddle   = "cuddle"    // head stroke or the cuddle button
	KindPlay     = "play"      // a game
	KindTooTired = "too_tired" // no energy to play
	KindLimit    = "limit"     // today's play time is used up
	KindNap      = "nap"
	KindNotTired = "not_tired" // refuses a nap
	KindWake     = "wake"      // woken from a nap
	KindAsleep   = "asleep"    // it is night: only a sleepy answer
	KindShake    = "shake"     // the robot was shaken
)

// awake is the common start of an action: needs up to date, night answers
// sleepily, a nap ends.
func (p *Pet) awake(now time.Time) (Reaction, bool) {
	p.Advance(now)
	if p.Settings.PhaseAt(now) == Night {
		return Reaction{Kind: KindAsleep}, false
	}
	if p.Napping(now) {
		p.NapUntil = time.Time{}
	}
	return Reaction{}, true
}

// Feed gives the pet a food (see Foods); unknown foods count as an apple.
func (p *Pet) Feed(now time.Time, food string) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	f, ok := Foods[food]
	if !ok {
		food, f = "apple", Foods["apple"]
	}
	if p.Stats.Food >= 90 {
		p.log(now, KindFull, food)
		return Reaction{Kind: KindFull, Food: food}
	}
	p.Stats.Food += f.Food
	p.Stats.Fun += f.Fun
	p.Stats.Energy += f.Energy
	p.clamp()
	p.log(now, "feed", food)
	return Reaction{Kind: KindEat, Food: food, Changed: true}
}

const cuddleEvery = 4 * time.Second

// Cuddle is a head stroke. Stroking without a break raises fun only every few seconds.
func (p *Pet) Cuddle(now time.Time) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	if p.PlayLeft(now) == 0 {
		return Reaction{Kind: KindLimit}
	}
	p.countPlay(now)
	if now.Sub(p.LastCuddle) < cuddleEvery {
		return Reaction{Kind: KindCuddle}
	}
	p.LastCuddle = now
	p.Stats.Fun += 6
	p.clamp()
	p.log(now, "cuddle", "")
	return Reaction{Kind: KindCuddle, Changed: true}
}

// Play is a game: fun for energy and a little food.
func (p *Pet) Play(now time.Time) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	if p.PlayLeft(now) == 0 {
		return Reaction{Kind: KindLimit}
	}
	if p.Stats.Energy < 15 {
		return Reaction{Kind: KindTooTired}
	}
	p.countPlay(now)
	p.Stats.Fun += 20
	p.Stats.Energy -= 8
	p.Stats.Food -= 4
	p.clamp()
	p.log(now, "play", "")
	return Reaction{Kind: KindPlay, Changed: true}
}

// NapLength is how long a daytime nap lasts unless the kid wakes the pet.
const NapLength = 15 * time.Minute

// Nap puts the pet to sleep for NapLength during the day.
func (p *Pet) Nap(now time.Time) Reaction {
	p.Advance(now)
	switch {
	case p.Settings.PhaseAt(now) == Night:
		return Reaction{Kind: KindAsleep}
	case p.Napping(now):
		return Reaction{Kind: KindNap}
	case p.Stats.Energy >= 80:
		return Reaction{Kind: KindNotTired}
	}
	p.NapUntil = now.Add(NapLength)
	p.log(now, "nap", "")
	return Reaction{Kind: KindNap, Changed: true}
}

// Wake ends a nap early.
func (p *Pet) Wake(now time.Time) Reaction {
	p.Advance(now)
	if !p.Napping(now) {
		return Reaction{Kind: KindWake}
	}
	p.NapUntil = time.Time{}
	return Reaction{Kind: KindWake, Changed: true}
}

// Shake: the robot was shaken. A little fun, no play time.
func (p *Pet) Shake(now time.Time) Reaction {
	if r, ok := p.awake(now); !ok {
		return r
	}
	p.Stats.Fun += 2
	p.clamp()
	return Reaction{Kind: KindShake, Changed: true}
}

// Food is what one serving adds.
type Food struct {
	Food, Fun, Energy float64
}

// Foods by key; FoodOrder is the order on the kid's page.
var Foods = map[string]Food{
	"apple":  {Food: 20, Fun: 2},
	"carrot": {Food: 20, Energy: 3},
	"banana": {Food: 25, Energy: 5},
	"bread":  {Food: 30},
	"milk":   {Food: 15, Energy: 10},
	"cake":   {Food: 15, Fun: 15},
}

var FoodOrder = []string{"apple", "carrot", "banana", "bread", "milk", "cake"}
