package app

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os/exec"
	"time"

	"github.com/mj41/stackchan-pet/internal/pet"
	"github.com/mj41/stackchan-pet/internal/sound"
	"github.com/mj41/stackchan-server/wire"
)

// The pet speaks its lines: espeak-ng (Config.Espeak) turns them into a high, childlike
// robot voice; the PCM is streamed to the robot's speaker at speaking pace (the robot
// buffers only about 3 s). A new line cuts the previous one off.

const (
	voiceChunk = sound.Rate / 2 // samples per message: half a second
	voiceAhead = 2              // messages sent at once before pacing (about 1 s)
	voiceCache = 64             // synthesized lines kept
)

// voiceArgs are espeak-ng's options per language: voice, pitch (0-99), words per minute.
var voiceArgs = map[string][]string{
	"cs": {"-v", "cs", "-p", "75", "-s", "150"},
	"en": {"-v", "en-us", "-p", "75", "-s", "160"},
}

// speak says a line on the robot (if sounds and the voice are on; at night only when
// atNight, e.g. the good night). Runs espeak-ng outside the lock.
func (a *App) speak(r *robot, line string, atNight bool) {
	s := r.pet.Settings
	if a.cfg.Espeak == "" || r.conn == nil || !s.Sounds || !s.Voice || (!atNight && s.PhaseAt(a.now()) == pet.Night) {
		return
	}
	r.voiceGen++
	gen, c, lang := r.voiceGen, r.conn, s.Lang
	go func() {
		pcm, err := a.synth(line, lang)
		if err != nil {
			a.log.Warn("voice", "err", err)
			return
		}
		for off, n := 0, 0; off < len(pcm); off, n = off+voiceChunk, n+1 {
			if n >= voiceAhead {
				time.Sleep(time.Second * voiceChunk / sound.Rate)
			}
			a.mu.Lock()
			current := r.voiceGen == gen && r.conn == c
			a.mu.Unlock()
			if !current {
				return // a newer line, or the robot left
			}
			c.binary(wire.BinSpeakerPCM, sound.Message(pcm[off:min(off+voiceChunk, len(pcm))]))
		}
	}()
}

// synth returns the line as 16 kHz mono PCM (cached).
func (a *App) synth(line, lang string) ([]int16, error) {
	key := lang + "\x00" + line
	a.voiceMu.Lock()
	if pcm, ok := a.voices[key]; ok {
		a.voiceMu.Unlock()
		return pcm, nil
	}
	a.voiceMu.Unlock()

	args, ok := voiceArgs[lang]
	if !ok {
		args = voiceArgs["en"]
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, a.cfg.Espeak, append(append([]string{}, args...), "--stdout", line)...).Output()
	if err != nil {
		return nil, err
	}
	pcm, rate, err := wavPCM(out)
	if err != nil {
		return nil, err
	}
	pcm = resample(pcm, rate, sound.Rate)

	a.voiceMu.Lock()
	if len(a.voices) >= voiceCache {
		clear(a.voices) // simple: start over
	}
	a.voices[key] = pcm
	a.voiceMu.Unlock()
	return pcm, nil
}

// wavPCM reads 16-bit mono PCM from a WAV (espeak-ng's streamed WAV may carry
// bogus sizes: the data runs to the end).
func wavPCM(b []byte) ([]int16, int, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, 0, errors.New("not a WAV")
	}
	rate := 0
	for p := 12; p+8 <= len(b); {
		id, size := string(b[p:p+4]), int(binary.LittleEndian.Uint32(b[p+4:]))
		p += 8
		switch id {
		case "fmt ":
			if p+16 > len(b) || binary.LittleEndian.Uint16(b[p:]) != 1 || binary.LittleEndian.Uint16(b[p+2:]) != 1 ||
				binary.LittleEndian.Uint16(b[p+14:]) != 16 {
				return nil, 0, errors.New("want 16-bit mono PCM")
			}
			rate = int(binary.LittleEndian.Uint32(b[p+4:]))
		case "data":
			data := b[p:]
			if size >= 0 && size < len(data) {
				data = data[:size]
			}
			pcm := make([]int16, len(data)/2)
			binary.Read(bytes.NewReader(data[:2*len(pcm)]), binary.LittleEndian, pcm)
			if rate == 0 {
				return nil, 0, errors.New("no fmt chunk")
			}
			return pcm, rate, nil
		}
		p += size + size&1
	}
	return nil, 0, errors.New("no data chunk")
}

// resample converts PCM between rates (linear interpolation; fine for speech).
func resample(in []int16, from, to int) []int16 {
	if from == to || len(in) == 0 {
		return in
	}
	n := len(in) * to / from
	out := make([]int16, n)
	for i := range out {
		x := float64(i) * float64(from) / float64(to)
		j := int(x)
		t := x - float64(j)
		a, b := float64(in[min(j, len(in)-1)]), float64(in[min(j+1, len(in)-1)])
		out[i] = int16(a + (b-a)*t)
	}
	return out
}
