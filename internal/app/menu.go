package app

import (
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
)

// The menu on the robot's screen: a long press opens it over a dimmed face. Its tiles
// are sprites marked "tap"; the robot reports which one a tap hit (screen_tap's
// "sprite"). Still pictures, no animation. Everything here runs with a.mu held.

const menuTimeout = 15 * time.Second

// menuItems: sprite id -> tile picture and its place (center) on the 320x240 screen.
var menuItems = []struct {
	id, asset string
	x, y      int
}{
	{"menu:play", "menu-play.png", 86, 66},
	{"menu:needs", "menu-needs.png", 234, 66},
	{"menu:nap", "menu-nap.png", 86, 170},
	{"menu:close", "menu-close.png", 234, 170},
}

// canMenu: the robot shows sprites and has the menu pictures.
func (r *robot) canMenu() bool {
	if !r.canSprite(assetDir + "menu-bg.png") {
		return false
	}
	for _, m := range menuItems {
		if !r.canSprite(assetDir + m.asset) {
			return false
		}
	}
	return true
}

func (a *App) openMenu(r *robot, now time.Time) {
	r.gen++ // drop pending steps of earlier reactions
	r.menuOpen = true
	r.menuID++
	r.busyUntil = now.Add(menuTimeout + time.Second)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	a.emotion(r, "happy")
	a.sprite(r, map[string]any{"id": "menu:bg", "asset": assetDir + "menu-bg.png", "x": 160, "y": 120, "z": 20})
	for _, m := range menuItems {
		a.sprite(r, map[string]any{"id": m.id, "asset": assetDir + m.asset, "x": m.x, "y": m.y, "z": 21, "tap": true})
	}
	id := r.menuID
	time.AfterFunc(menuTimeout, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.menuOpen && r.menuID == id && r.conn != nil {
			a.closeMenu(r)
			a.express(r, a.now())
		}
	})
}

func (a *App) closeMenu(r *robot) {
	r.menuOpen = false
	r.busyUntil = time.Time{}
	a.clearSprites(r)
}

// menuTap handles a tap while the menu is open: the tile it hit, or close.
func (a *App) menuTap(r *robot, sprite string, now time.Time) {
	a.closeMenu(r)
	p := r.pet
	var re pet.Reaction
	switch sprite {
	case "menu:play":
		re = a.playAction(r, now)
	case "menu:needs":
		p.Advance(now)
		a.showNeeds(r, now)
		a.publishState(r)
		return
	case "menu:nap":
		re = p.Nap(now)
	default: // the X, or a tap next to the tiles
		a.express(r, now)
		return
	}
	a.dirty = true
	a.react(r, re, now)
	a.publishReaction(r, re)
	a.publishState(r)
}
