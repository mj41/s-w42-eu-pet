package app

import (
	"strings"
	"testing"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
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
	for c := range colorButtons {
		files = append(files, "pet/color-"+c+".png")
	}
	haveFiles(t, r, files...)
	r.event("screen_tap", map[string]any{"x": 160, "y": 120}) // the main menu
	r.event("screen_tap", map[string]any{"x": 208, "y": 50, "sprite": "m:open:play"})
	time.Sleep(50 * time.Millisecond)
	r.event("screen_tap", map[string]any{"x": 160, "y": 100, "sprite": "m:play:colors"})

	for round := 1; round <= pet.GameRounds; round++ {
		// The swatches at the top say what to press, in order.
		var seq []string
		for len(seq) < colorsPerRound[round-1] {
			args := r.command("sprite")
			id, _ := args["id"].(string)
			if strings.HasPrefix(id, "c:s") && args["asset"] != nil {
				seq = append(seq, strings.TrimSuffix(strings.TrimPrefix(args["asset"].(string), assetDir+"color-"), ".png"))
			}
		}
		if round == 2 { // a wrong button first: only a buzz
			for c := range colorButtons {
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
		return body.Command == "say" && strings.Contains(text, "Hotovo za") && strings.Contains(text, "sekund")
	})
	e.robotState("robot-1", func(rb *robot) {
		if rb.game != nil || rb.pet.ColorsBestMs == 0 {
			t.Fatalf("after the game: game %v, best %d ms", rb.game != nil, rb.pet.ColorsBestMs)
		}
	})
}

func TestColorPromptAndSeconds(t *testing.T) {
	if s := colorPrompt("cs", []string{"red", "blue", "green"}); s != "Červená, modrá a zelená!" {
		t.Errorf("prompt: %q", s)
	}
	if s := colorPrompt("en", []string{"yellow"}); s != "Yellow!" {
		t.Errorf("prompt: %q", s)
	}
	for d, want := range map[time.Duration]string{time.Second: "1 sekundu", 3 * time.Second: "3 sekundy", 14 * time.Second: "14 sekund"} {
		if s := secondsText("cs", d); s != want {
			t.Errorf("%v: %q, want %q", d, s, want)
		}
	}
}
