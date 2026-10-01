package app

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
)

// Debugging from the terminal (LAN development): with the robot token as a bearer,
//
//	POST /api/debug/{id}/run {"action": "feed", "food": "banana", "shots_ms": [200, 600, 1200]}
//
// runs a kid's action on the robot's pet (optional) and asks the robot for screen
// snapshots at those moments. The robot sends them as JPEGs (binary 0x07); they are
// saved in Config.DebugDir as <robot>-<time>.jpg (the newest 40 are kept).

const keepScreens = 40

func (a *App) handleDebugRun(w http.ResponseWriter, r *http.Request) {
	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !a.tokenOK(token) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req struct {
		Action   string `json:"action"`
		Food     string `json:"food"`
		Menu     string `json:"menu"` // for "menu": which one (default main)
		ShotsMs  []int  `json:"shots_ms"`
		Commands []struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		} `json:"commands"` // raw robot commands, sent first
	}
	if !readJSON(w, r, &req) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rb := a.robots[r.PathValue("id")]
	if rb == nil || rb.conn == nil {
		http.Error(w, "robot offline", http.StatusConflict)
		return
	}
	for _, c := range req.Commands {
		rb.conn.command(c.Command, c.Args)
	}
	now := a.now()
	var re pet.Reaction
	switch req.Action {
	case "":
	case "feed":
		re = rb.pet.Feed(now, req.Food)
	case "cuddle":
		re = rb.pet.Cuddle(now)
	case "play":
		re = a.playAction(rb, now)
	case "dream":
		rb.lastAsleep = time.Time{}
		a.dream(rb, now)
	case "express":
		a.express(rb, now)
	case "say": // a spoken line (even at night), e.g. to watch the robot's memory
		saved := rb.pet.Settings.Sounds
		rb.pet.Settings.Sounds = true
		a.sayLine(rb, text(rb.pet.Settings.Lang, "hungry", ""), 3, true)
		rb.pet.Settings.Sounds = saved
	case "menu":
		if _, ok := menus[req.Menu]; !ok {
			req.Menu = "main"
		}
		a.openMenu(rb, now, req.Menu)
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if re.Kind != "" {
		a.dirty = true
		a.react(rb, re, now)
		a.publishReaction(rb, re)
		a.publishState(rb)
	}
	for _, ms := range req.ShotsMs {
		conn := rb.conn
		time.AfterFunc(time.Duration(ms)*time.Millisecond, func() { conn.command("screen_snapshot", nil) })
	}
	files := 0
	for range rb.files {
		files++
	}
	writeJSON(w, http.StatusOK, map[string]any{"reaction": re, "shots": len(req.ShotsMs), "dir": a.cfg.DebugDir,
		"commands": rb.commands, "files": files, "sprites": rb.canSprite(assetDir + "ball.png"), "face": rb.faceShown})
}

// saveScreen keeps a screen snapshot from the robot (binary 0x07) in DebugDir.
func (a *App) saveScreen(id string, jpeg []byte) {
	if a.cfg.DebugDir == "" {
		return
	}
	if err := os.MkdirAll(a.cfg.DebugDir, 0o755); err != nil {
		return
	}
	name := filepath.Join(a.cfg.DebugDir, fmt.Sprintf("%s-%s.jpg", id, time.Now().Format("150405.000")))
	if err := os.WriteFile(name, jpeg, 0o644); err != nil {
		return
	}
	files, _ := filepath.Glob(filepath.Join(a.cfg.DebugDir, "*.jpg"))
	sort.Strings(files)
	for len(files) > keepScreens {
		os.Remove(files[0])
		files = files[1:]
	}
}
