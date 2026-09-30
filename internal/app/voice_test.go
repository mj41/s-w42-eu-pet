package app

import (
	"os/exec"
	"testing"

	"github.com/mj41/stackchan-pet/internal/sound"
)

func TestResample(t *testing.T) {
	in := make([]int16, 22050)
	if out := resample(in, 22050, sound.Rate); len(out) != sound.Rate {
		t.Fatalf("one second at 22050 Hz -> %d samples at 16 kHz", len(out))
	}
	if out := resample(in, 16000, 16000); len(out) != len(in) {
		t.Fatal("same rate should keep the samples")
	}
}

func TestSynthWithEspeak(t *testing.T) {
	path, err := exec.LookPath("espeak-ng")
	if err != nil {
		t.Skip("espeak-ng not installed")
	}
	a := New(Config{Espeak: path})
	pcm, err := a.synth("Mám hlad!", "cs")
	if err != nil {
		t.Fatal(err)
	}
	if secs := float64(len(pcm)) / sound.Rate; secs < 0.3 || secs > 3 {
		t.Fatalf("\"Mám hlad!\" took %.2f s", secs)
	}
	if again, _ := a.synth("Mám hlad!", "cs"); &again[0] != &pcm[0] {
		t.Fatal("a line should come from the cache the second time")
	}
}
