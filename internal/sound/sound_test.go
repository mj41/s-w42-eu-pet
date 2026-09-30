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
