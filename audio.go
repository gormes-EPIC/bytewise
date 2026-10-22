package main

// Procedurally generated music and sound effects. Everything is synthesised at
// startup, so the binary needs no audio assets.

import (
	"bytes"
	"math"

	"github.com/hajimehoshi/ebiten/v2/audio"
)

const sampleRate = 44100

// ---------- synthesis ----------

// track is a mono buffer of float samples in [-1, 1].
type track []float64

func newTrack(seconds float64) track { return make(track, int(seconds*sampleRate)) }

type wave func(phase float64) float64

func sine(p float64) float64 { return math.Sin(2 * math.Pi * p) }

func triangle(p float64) float64 {
	p -= math.Floor(p)
	return 4*math.Abs(p-0.5) - 1
}

// warm is a sine with a little second and third harmonic: soft, organ-like.
func warm(p float64) float64 { return 0.7*sine(p) + 0.25*sine(2*p) + 0.05*sine(3*p) }

// note mixes a tone into the track. It starts at t0 seconds, glides from freq
// to freq2 over the hold time, fades in over attack, holds, then decays
// exponentially with the given time constant.
func (t track) note(t0, hold, freq, freq2, amp, attack, decay float64, w wave) {
	start := int(t0 * sampleRate)
	n := int((hold + decay*6) * sampleRate)
	phase := 0.0
	for i := 0; i < n; i++ {
		idx := start + i
		if idx < 0 || idx >= len(t) {
			break
		}
		s := float64(i) / sampleRate
		env := 1.0
		if attack > 0 && s < attack {
			env = s / attack
		}
		if s > hold {
			env *= math.Exp(-(s - hold) / decay)
		}
		f := freq
		if hold > 0 {
			f += (freq2 - freq) * math.Min(1, s/hold)
		} else {
			f = freq2
		}
		phase += f / sampleRate
		t[idx] += amp * env * w(phase)
	}
}

// delay adds a feedback echo.
func (t track) delay(seconds, feedback, mix float64) {
	d := int(seconds * sampleRate)
	if d <= 0 || d >= len(t) {
		return
	}
	buf := make([]float64, len(t))
	for i := range t {
		echo := 0.0
		if i >= d {
			echo = buf[i-d]
		}
		buf[i] = t[i] + echo*feedback
		t[i] += echo * mix
	}
}

// trim drops trailing near-silence.
func (t track) trim() track {
	end := len(t)
	for end > 0 && math.Abs(t[end-1]) < 0.002 {
		end--
	}
	return t[:end+min(len(t)-end, sampleRate/50)]
}

// toPCM converts to 16-bit little-endian stereo with gentle soft clipping.
func (t track) toPCM() []byte {
	out := make([]byte, len(t)*4)
	for i, v := range t {
		s := int16(math.Tanh(v) * 32000)
		lo, hi := byte(s), byte(s>>8)
		out[i*4], out[i*4+1], out[i*4+2], out[i*4+3] = lo, hi, lo, hi
	}
	return out
}

func midi(n int) float64 { return 440 * math.Pow(2, float64(n-69)/12) }

// ---------- music ----------

// composeMusic renders a calm, seamlessly looping pentatonic piece:
// a soft pad, a sine bass and a plucked arpeggio with a dotted-eighth echo.
func composeMusic() []byte {
	const bpm = 76.0
	beat := 60 / bpm
	eighth := beat / 2
	const bars = 8
	loopLen := float64(bars) * 4 * beat

	// Two bars per chord: C, Am, F, G (as MIDI notes).
	type chord struct {
		bass int
		pad  []int
		arp  []int
	}
	chords := []chord{
		{48, []int{60, 64, 67}, []int{72, 76, 79, 84}},
		{45, []int{57, 60, 64}, []int{69, 72, 76, 81}},
		{41, []int{57, 60, 65}, []int{69, 72, 77, 81}},
		{43, []int{55, 59, 62}, []int{67, 71, 74, 79}},
	}
	// Arpeggio patterns, one entry per eighth note over two bars; -1 is a rest.
	patternA := []int{0, 1, 2, 3, 2, 1, 0, 2, 1, 2, 3, 2, 1, 0, -1, -1}
	patternB := []int{0, 2, 1, 3, 2, 0, 1, 2, 3, 2, 1, 0, 2, -1, 1, -1}

	// Render two passes so the echo tail of pass one is present at the start
	// of pass two, then keep pass two: it loops without a seam.
	t := newTrack(loopLen*2 + 2)
	for pass := 0; pass < 2; pass++ {
		base := float64(pass) * loopLen
		for ci, c := range chords {
			t0 := base + float64(ci)*8*beat
			for _, n := range c.pad {
				t.note(t0, 8*beat-0.3, midi(n), midi(n), 0.055, 0.9, 0.5, warm)
			}
			for b := 0; b < 4; b++ {
				t.note(t0+float64(b)*2*beat, 1.6*beat, midi(c.bass), midi(c.bass), 0.16, 0.02, 0.25, sine)
			}
			pattern := patternA
			if ci >= 2 {
				pattern = patternB
			}
			for i, idx := range pattern {
				if idx < 0 {
					continue
				}
				t.note(t0+float64(i)*eighth, 0, midi(c.arp[idx]), midi(c.arp[idx]), 0.13, 0.004, 0.22, triangle)
			}
			// a sparkle two octaves up on the first beat of each chord
			t.note(t0, 0, midi(c.arp[0]+12), midi(c.arp[0]+12), 0.035, 0.004, 0.6, sine)
		}
	}
	t.delay(eighth*1.5, 0.38, 0.32)
	start := int(loopLen * sampleRate)
	return t[start : start+start].toPCM()
}

// ---------- sound effects ----------

func makeEffects() map[string][]byte {
	fx := map[string]func(t track){
		"move": func(t track) {
			t.note(0, 0, 523, 523, 0.3, 0.002, 0.06, sine)
		},
		"turn": func(t track) {
			t.note(0, 0, 880, 880, 0.22, 0.001, 0.03, sine)
		},
		"jump": func(t track) {
			t.note(0, 0.12, 330, 660, 0.28, 0.005, 0.08, triangle)
		},
		"activate": func(t track) {
			t.note(0, 0, 659, 659, 0.3, 0.003, 0.45, warm)
			t.note(0.09, 0, 784, 784, 0.3, 0.003, 0.6, warm)
		},
		"deactivate": func(t track) {
			t.note(0, 0, 784, 784, 0.22, 0.003, 0.3, warm)
			t.note(0.09, 0, 659, 659, 0.22, 0.003, 0.4, warm)
		},
		"blocked": func(t track) {
			t.note(0, 0.08, 110, 95, 0.5, 0.002, 0.1, sine)
			t.note(0, 0.05, 165, 150, 0.18, 0.002, 0.06, triangle)
		},
		"win": func(t track) {
			for i, n := range []int{72, 76, 79, 84} {
				t.note(float64(i)*0.12, 0, midi(n), midi(n), 0.3, 0.003, 0.8, warm)
			}
			t.note(0.48, 0, midi(88), midi(88), 0.18, 0.003, 1.0, sine)
		},
		"fail": func(t track) {
			t.note(0, 0.15, 392, 392, 0.28, 0.01, 0.3, warm)
			t.note(0.25, 0.2, 311, 311, 0.28, 0.01, 0.45, warm)
		},
		"place": func(t track) {
			t.note(0, 0.03, 600, 820, 0.28, 0.002, 0.05, sine)
		},
		"remove": func(t track) {
			t.note(0, 0.03, 700, 450, 0.24, 0.002, 0.05, sine)
		},
		"click": func(t track) {
			t.note(0, 0, 1200, 1200, 0.18, 0.001, 0.02, sine)
		},
	}
	out := make(map[string][]byte, len(fx))
	for name, gen := range fx {
		t := newTrack(3)
		gen(t)
		out[name] = t.trim().toPCM()
	}
	return out
}

// ---------- playback ----------

type sound struct {
	ctx     *audio.Context
	music   *audio.Player
	effects map[string][]byte
	musicOn bool
	sfxOn   bool
}

// newSound synthesises everything. With enabled=false it returns a silent
// stub that never touches the audio device.
func newSound(enabled bool) *sound {
	if !enabled {
		return &sound{}
	}
	s := &sound{ctx: audio.NewContext(sampleRate), effects: makeEffects()}
	pcm := composeMusic()
	loop := audio.NewInfiniteLoop(bytes.NewReader(pcm), int64(len(pcm)))
	if p, err := s.ctx.NewPlayer(loop); err == nil {
		p.SetVolume(0.45)
		s.music = p
	}
	return s
}

func (s *sound) play(name string) {
	if s.ctx == nil || !s.sfxOn {
		return
	}
	pcm, ok := s.effects[name]
	if !ok {
		return
	}
	p := s.ctx.NewPlayerFromBytes(pcm)
	p.SetVolume(0.7)
	p.Play()
}

func (s *sound) setMusic(on bool) {
	s.musicOn = on
	if s.music == nil {
		return
	}
	if on {
		s.music.Play()
	} else {
		s.music.Pause()
	}
}
