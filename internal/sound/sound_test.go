package sound

import "testing"

func TestSoundsFitTheRobotBuffer(t *testing.T) {
	for _, name := range Names {
		pcm := PCM(name)
		if len(pcm) == 0 {
			t.Errorf("%s: empty", name)
		}
		if secs := float64(len(pcm)) / Rate; secs > 2.8 {
			t.Errorf("%s: %.2f s, the robot keeps only 3 s", name, secs)
		}
		if m := Message(pcm); len(m) != 2+2*len(pcm) || m[0] != 0x80 || m[1] != 0x3E {
			t.Errorf("%s: bad message header % x", name, m[:2])
		}
	}
	if PCM("nope") != nil {
		t.Error("unknown sound should be nil")
	}
}

func TestWAV(t *testing.T) {
	w := WAV([]int16{1, -2, 3})
	if len(w) != 50 || string(w[:4]) != "RIFF" || string(w[8:16]) != "WAVEfmt " || string(w[36:40]) != "data" || w[44] != 1 || w[46] != 0xFE {
		t.Fatalf("WAV header or data wrong: % x", w)
	}
}
