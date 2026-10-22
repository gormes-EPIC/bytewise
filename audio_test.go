package main

import (
	"encoding/binary"
	"math"
	"testing"
)

func pcmPeak(b []byte) (peak float64, nonZero bool) {
	for i := 0; i+1 < len(b); i += 2 {
		v := math.Abs(float64(int16(binary.LittleEndian.Uint16(b[i:])))) / 32768
		peak = math.Max(peak, v)
		nonZero = nonZero || v > 0
	}
	return peak, nonZero
}

func TestEffectsAreShortAndAudible(t *testing.T) {
	fx := makeEffects()
	for _, name := range []string{"move", "turn", "jump", "activate", "deactivate", "blocked", "win", "fail", "place", "remove", "click"} {
		pcm, ok := fx[name]
		if !ok {
			t.Fatalf("missing effect %q", name)
		}
		secs := float64(len(pcm)) / 4 / sampleRate
		if secs < 0.02 || secs > 3 {
			t.Errorf("%s: %.2fs long", name, secs)
		}
		peak, nonZero := pcmPeak(pcm)
		if !nonZero || peak < 0.1 || peak > 0.98 {
			t.Errorf("%s: peak %.2f", name, peak)
		}
	}
}

func TestMusicLoopsSeamlessly(t *testing.T) {
	pcm := composeMusic()
	secs := float64(len(pcm)) / 4 / sampleRate
	if secs < 20 || secs > 40 {
		t.Fatalf("loop is %.1fs", secs)
	}
	peak, _ := pcmPeak(pcm)
	if peak < 0.3 || peak > 0.98 {
		t.Errorf("peak %.2f", peak)
	}
	// The end of the loop should flow into its start: compare the RMS level of
	// the last and first 50 ms, which would differ wildly if the echo tail were
	// missing at the start.
	rms := func(b []byte) float64 {
		sum := 0.0
		n := 0
		for i := 0; i+1 < len(b); i += 4 {
			v := float64(int16(binary.LittleEndian.Uint16(b[i:]))) / 32768
			sum += v * v
			n++
		}
		return math.Sqrt(sum / float64(n))
	}
	w := sampleRate / 20 * 4
	head, tail := rms(pcm[:w]), rms(pcm[len(pcm)-w:])
	if head == 0 || tail == 0 || head/tail > 4 || tail/head > 4 {
		t.Errorf("loop seam: head rms %.3f, tail rms %.3f", head, tail)
	}
}
