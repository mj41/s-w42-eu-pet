// Package sound synthesizes the pet's short sounds as 16 kHz mono PCM, ready
// for the robot's speaker message (wire.BinSpeakerPCM). The robot buffers at
// most 3 s, so every sound is shorter than that.
package sound

import (
	"encoding/binary"
	"math"
	"math/rand/v2"
)

// Rate is the sample rate of every sound.
const Rate = 16000

// note is one tone: frequency in Hz (0 = rest), length in ms.
type note struct {
	hz, ms float64
}

// Names of the sounds.
const (
	Chirp   = "chirp"   // happy, a cuddle
	Munch   = "munch"   // eating
	Tada    = "tada"    // playing
	No      = "no"      // refusing
	Yawn    = "yawn"    // tired, going to nap or to bed
	Morning = "morning" // waking up
	Lullaby = "lullaby" // good night
	Hello   = "hello"   // someone came close
	Whee    = "whee"    // shaken
)

// Names lists every sound.
var Names = []string{Chirp, Munch, Tada, No, Yawn, Morning, Lullaby, Hello, Whee}

// Musical notes (Hz).
const (
	c5, d5, e5, f5, g5, a5, b5, c6, e6, g6 = 523.25, 587.33, 659.25, 698.46, 783.99, 880.0, 987.77, 1046.5, 1318.5, 1568.0
	g4, a4                                 = 392.0, 440.0
)

// PCM returns the sound as s16le samples at Rate, or nil for an unknown name.
func PCM(name string) []int16 {
	switch name {
	case Chirp:
		return melody([]note{{c6, 70}, {e6, 70}, {g6, 110}}, 0.9)
	case Munch:
		var out []int16
		for range 3 {
			out = append(out, noise(90, 0.5)...)
			out = append(out, silence(110)...)
		}
		return out
	case Tada:
		return melody([]note{{c5, 110}, {e5, 110}, {g5, 110}, {c6, 350}}, 1)
	case No:
		return melody([]note{{a4, 180}, {0, 40}, {g4, 300}}, 0.9)
	case Yawn:
		return glide(700, 300, 900, 0.6)
	case Morning:
		return melody([]note{{g5, 150}, {c6, 150}, {e6, 150}, {0, 60}, {e6, 120}, {g6, 400}}, 0.8)
	case Lullaby: // slow and soft
		return melody([]note{{g5, 350}, {e5, 350}, {g5, 350}, {e5, 350}, {f5, 300}, {d5, 300}, {c5, 700}}, 0.45)
	case Hello:
		return melody([]note{{g5, 110}, {c6, 180}}, 0.8)
	case Whee:
		return glide(400, 1400, 450, 0.7)
	}
	return nil
}

// Message is the robot's speaker message body: uint16 LE rate, then s16le PCM
// (without the leading type byte).
func Message(pcm []int16) []byte {
	b := make([]byte, 2+2*len(pcm))
	binary.LittleEndian.PutUint16(b, Rate)
	for i, s := range pcm {
		binary.LittleEndian.PutUint16(b[2+2*i:], uint16(s))
	}
	return b
}

const peak = 0.35 * math.MaxInt16 // headroom: the robot's small speaker distorts when loud

func samples(ms float64) int { return int(ms * Rate / 1000) }

func silence(ms float64) []int16 { return make([]int16, samples(ms)) }

// envelope softens both ends of a tone so it does not click.
func envelope(i, n int) float64 {
	a := samples(8)
	switch {
	case i < a:
		return float64(i) / float64(a)
	case i > n-4*a:
		return max(0, float64(n-i)/float64(4*a))
	}
	return 1
}

// melody plays notes with a soft square-ish tone (sine plus a little third harmonic).
func melody(notes []note, gain float64) []int16 {
	var out []int16
	for _, nt := range notes {
		n := samples(nt.ms)
		for i := range n {
			v := 0.0
			if nt.hz > 0 {
				ph := 2 * math.Pi * nt.hz * float64(i) / Rate
				v = (math.Sin(ph) + 0.25*math.Sin(3*ph)) / 1.25 * envelope(i, n)
			}
			out = append(out, int16(v*gain*peak))
		}
	}
	return out
}

// glide sweeps from one frequency to another.
func glide(fromHz, toHz, ms, gain float64) []int16 {
	n := samples(ms)
	out := make([]int16, n)
	ph := 0.0
	for i := range n {
		f := fromHz + (toHz-fromHz)*float64(i)/float64(n)
		ph += 2 * math.Pi * f / Rate
		out[i] = int16(math.Sin(ph) * envelope(i, n) * gain * peak)
	}
	return out
}

// noise is a short crunchy burst (a bite).
func noise(ms, gain float64) []int16 {
	n := samples(ms)
	out := make([]int16, n)
	r := rand.New(rand.NewPCG(1, 2)) // the same bite every time
	prev := 0.0
	for i := range n {
		prev = 0.6*prev + 0.4*(r.Float64()*2-1) // low-passed: less hiss
		out[i] = int16(prev * envelope(i, n) * gain * peak * 2)
	}
	return out
}
