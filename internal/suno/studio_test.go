package suno

import (
	"encoding/json"
	"math"
	"os"
	"testing"
)

// TestDownbeatsMatchesCapture checks the beat-grid builder against a real
// render-state payload captured from a Suno session: same tempo and clip range
// must yield byte-for-byte the same downbeats Suno's own client sent.
func TestDownbeatsMatchesCapture(t *testing.T) {
	raw, err := os.ReadFile("testdata/render_state_request.json")
	if err != nil {
		t.Skipf("no capture fixture: %v", err)
	}
	var body struct {
		StartBeats float64      `json:"start_beats"`
		EndBeats   float64      `json:"end_beats"`
		Downbeats  [][2]float64 `json:"downbeats"`
		State      struct {
			Timing struct {
				BPS float64 `json:"bps"`
			} `json:"timing"`
			TimeSignatureChanges []struct {
				SubdivisionsPerBar int `json:"subdivisionsPerBar"`
			} `json:"timeSignatureChanges"`
			Tracks []struct {
				Clips []struct {
					StartBeats float64 `json:"startBeats"`
					EndBeats   float64 `json:"endBeats"`
				} `json:"clips"`
			} `json:"tracks"`
		} `json:"state"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	clip := body.State.Tracks[0].Clips[0]
	beatsPerBar := body.State.TimeSignatureChanges[0].SubdivisionsPerBar
	got := downbeats(clip.StartBeats, clip.EndBeats, body.State.Timing.BPS, beatsPerBar)

	if len(got) != len(body.Downbeats) {
		t.Fatalf("count: got %d, want %d", len(got), len(body.Downbeats))
	}
	for i := range got {
		if math.Abs(got[i][0]-body.Downbeats[i][0]) > 1e-6 {
			t.Fatalf("downbeat %d time: got %v, want %v", i, got[i][0], body.Downbeats[i][0])
		}
		if got[i][1] != body.Downbeats[i][1] {
			t.Fatalf("downbeat %d label: got %v, want %v", i, got[i][1], body.Downbeats[i][1])
		}
	}

	// start_beats/end_beats are just the clip's own range.
	if clip.StartBeats != body.StartBeats || clip.EndBeats != body.EndBeats {
		t.Fatalf("start/end mismatch: clip=(%v,%v) body=(%v,%v)",
			clip.StartBeats, clip.EndBeats, body.StartBeats, body.EndBeats)
	}
}
