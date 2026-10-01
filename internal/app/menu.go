package app

import (
	"strings"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
)

// Menus on the robot's screen: a tap (or a long press) opens the main menu over a
// dimmed face; tiles open the food and play menus or do things. The tiles are
// sprites marked "tap": the robot reports which one a tap hit (screen_tap's
// "sprite"). Still pictures. Everything here runs with a.mu held.

const menuTimeout = 15 * time.Second

// menuItem is a tile: its sprite id is "m:" + action; open:<menu> goes to another menu.
type menuItem struct {
	action, asset string
	x, y          int // center on the 320x240 screen
}

var menus = map[string][]menuItem{
	"main": {
		{"open:food", "menu-food.png", 56, 64},
		{"open:play", "menu-play.png", 160, 64},
		{"nap", "menu-nap.png", 264, 64},
		{"needs", "menu-needs.png", 108, 172},
		{"close", "menu-close.png", 212, 172},
	},
	"food": {
		{"feed:apple", "menu-food-apple.png", 56, 48},
		{"feed:carrot", "menu-food-carrot.png", 160, 48},
		{"feed:banana", "menu-food-banana.png", 264, 48},
		{"feed:bread", "menu-food-bread.png", 56, 132},
		{"feed:milk", "menu-food-milk.png", 160, 132},
		{"feed:cake", "menu-food-cake.png", 264, 132},
		{"open:main", "menu-back.png", 160, 206},
	},
	"play": {
		{"play:catch", "menu-catch.png", 90, 100},
		{"play:dance", "menu-dance.png", 230, 100},
		{"open:main", "menu-back.png", 160, 206},
	},
}

// canMenu: the robot shows sprites and has every menu picture.
func (r *robot) canMenu() bool {
	if !r.canSprite(assetDir + "menu-bg.png") {
		return false
	}
	for _, items := range menus {
		for _, m := range items {
			if !r.canSprite(assetDir + m.asset) {
				return false
			}
		}
	}
	return true
}

// openMenu shows a menu (replacing the one shown).
func (a *App) openMenu(r *robot, now time.Time, name string) {
	r.gen++ // drop pending steps of earlier reactions
	a.clearSprites(r)
	r.menu = name
	r.menuID++
	r.busyUntil = now.Add(menuTimeout + time.Second)
	if r.pictureOn {
		r.conn.command("face", nil)
		r.pictureOn = false
	}
	if name == "main" {
		a.emotion(r, "happy")
	}
	a.sprite(r, map[string]any{"id": "m:bg", "asset": assetDir + "menu-bg.png", "x": 160, "y": 120, "z": 20})
	for _, m := range menus[name] {
		a.sprite(r, map[string]any{"id": "m:" + m.action, "asset": assetDir + m.asset, "x": m.x, "y": m.y, "z": 21, "tap": true})
	}
	id := r.menuID
	time.AfterFunc(menuTimeout, func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.menu != "" && r.menuID == id && r.conn != nil {
			a.closeMenu(r)
			a.express(r, a.now())
		}
	})
}

func (a *App) closeMenu(r *robot) {
	r.menu = ""
	r.busyUntil = time.Time{}
	a.clearSprites(r)
}

// menuTap handles a tap while a menu is open: the tile it hit (its sprite id), or close.
func (a *App) menuTap(r *robot, sprite string, now time.Time) {
	action := strings.TrimPrefix(sprite, "m:")
	if menu, ok := strings.CutPrefix(action, "open:"); ok {
		if _, known := menus[menu]; known {
			a.openMenu(r, now, menu)
			return
		}
	}
	a.closeMenu(r)
	p := r.pet
	var re pet.Reaction
	switch {
	case strings.HasPrefix(action, "feed:"):
		re = p.Feed(now, strings.TrimPrefix(action, "feed:"))
	case action == "play:catch":
		re = a.playAction(r, now)
	case action == "play:dance":
		re = p.Play(now)
	case action == "nap":
		re = p.Nap(now)
	case action == "needs":
		p.Advance(now)
		a.showNeeds(r, now)
		a.publishState(r)
		return
	default: // the X, the backdrop, or a tap next to the tiles
		a.express(r, now)
		return
	}
	a.dirty = true
	a.react(r, re, now)
	a.publishReaction(r, re)
	a.publishState(r)
}
