package app

import (
	"strings"
	"testing"
	"time"

	"github.com/mj41/stackchan-pet/internal/robotpic"
	"github.com/mj41/stackchan-server/wire"
)

func TestColorGame(t *testing.T) {
	e := newEnv(t, "")
	e.fastGame(300 * time.Millisecond)
	r := e.connectRobotWith("robot-1", []string{"sprite", "assets"})
	kid := e.browser()
	kid.pair(r)
	files := []string{"pet/menu-bg.png"}
	for _, items := range menus {
		for _, m := range items {
			files = append(files, "pet/"+m.asset)
		}
	}
	for _, c := range robotpic.Colors {
		files = append(files, "pet/color-"+c+".png")
	}
	haveFiles(t, r, files...)
	r.event("screen_tap", map[string]any{"x": 160, "y": 120}) // the main menu
	r.event("screen_tap", map[string]any{"x": 208, "y": 50, "sprite": "m:open:play"})
	time.Sleep(50 * time.Millisecond)
	r.event("screen_tap", map[string]any{"x": 160, "y": 100, "sprite": "m:play:colors"})

	ledColor := map[string]string{}
	for c, hex := range robotpic.ColorLED {
		ledColor[hex] = c
	}
	for round, n := range []int{1, 2, 2, 3, 4} {
		// The LED strips say what to press, in order (colorParts).
		var pixels []any
		for pixels == nil { // the next round's (the left strip lit; pressed parts go dark)
			if args := r.command("leds"); args["pixels"] != nil && args["pixels"].([]any)[0] != "#000000" {
				pixels = args["pixels"].([]any)
			}
		}
		var seq []string
		for i, part := range colorParts(n) {
			seq = append(seq, ledColor[pixels[part[0]].(string)])
			if seq[i] == "" {
				t.Fatalf("round %d: part %d dark: %v", round+1, i, pixels)
			}
		}
		if n == 1 && pixels[6] != "#000000" {
			t.Fatalf("one color lights the right strip: %v", pixels)
		}
		if round == 2 && seq[0] != seq[1] {
			t.Fatalf("round 3 is a double tap: %v", seq)
		}
		if round == 1 { // a wrong button first: only a buzz
			for _, c := range robotpic.Colors {
				if c != seq[0] {
					r.event("screen_tap", map[string]any{"sprite": "c:" + c})
					break
				}
			}
		}
		for _, c := range seq {
			r.event("screen_tap", map[string]any{"sprite": "c:" + c})
		}
	}
	r.next("the time", func(m robotMsg) bool {
		var body wire.RobotCommandBody
		m.frame.Decode(&body)
		text, _ := body.Args["text"].(string)
		return body.Command == "say" && strings.Contains(text, "otovo za") && strings.Contains(text, "sekund")
	})
	e.robotState("robot-1", func(rb *robot) {
		if rb.game != nil || rb.pet.ColorsBestMs == 0 {
			t.Fatalf("after the game: game %v, best %d ms", rb.game != nil, rb.pet.ColorsBestMs)
		}
	})
}

func TestSecondsText(t *testing.T) {
	for d, want := range map[time.Duration]string{time.Second: "1 sekundu", 3 * time.Second: "3 sekundy", 14 * time.Second: "14 sekund"} {
		if s := secondsText("cs", d); s != want {
			t.Errorf("%v: %q, want %q", d, s, want)
		}
	}
}
