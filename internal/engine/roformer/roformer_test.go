package roformer

import (
	"context"
	"strings"
	"testing"
)

// The real shape of `audio-separator --list_models --list_format json`:
// {architecture: {display label: {filename, stems, ...}}}.
const sampleList = `{
  "VR": {
    "VR Arch Single Model v5: 1_HP-UVR": {
      "filename": "1_HP-UVR.pth",
      "stems": ["instrumental", "vocals"]
    }
  },
  "MDXC": {
    "Roformer Model: BS Roformer | Vocals Resurrection by unwa": {
      "filename": "bs_roformer_vocals_resurrection_unwa.ckpt",
      "stems": []
    },
    "Roformer Model: MelBand Roformer | Vocals by Kimberley Jensen": {
      "filename": "vocals_mel_band_roformer.ckpt",
      "stems": ["vocals", "other"]
    },
    "MDX23C Model: MDX23C-InstVoc HQ": {
      "filename": "MDX23C-8KFFT-InstVoc_HQ.ckpt",
      "stems": ["vocals", "instrumental"]
    }
  }
}`

func TestParseModelList(t *testing.T) {
	got := parseModelList(sampleList)

	byName := map[string]entry{}
	for _, e := range got {
		byName[e.checkpoint] = e
	}

	// Only RoFormer checkpoints belong in this backend's catalog; a .pth from
	// the VR architecture or a plain MDX23C model must not leak in.
	for _, unwanted := range []string{"1_HP-UVR.pth", "MDX23C-8KFFT-InstVoc_HQ.ckpt"} {
		if _, ok := byName[unwanted]; ok {
			t.Errorf("%s should not be in the RoFormer catalog", unwanted)
		}
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 RoFormer models, got %d: %v", len(got), byName)
	}

	bs, ok := byName["bs_roformer_vocals_resurrection_unwa.ckpt"]
	if !ok {
		t.Fatal("missing bs_roformer_vocals_resurrection_unwa.ckpt")
	}
	if bs.label != "BS Roformer | Vocals Resurrection by unwa" {
		t.Errorf("label = %q, want the 'Roformer Model: ' prefix stripped", bs.label)
	}
	if !strings.HasPrefix(bs.family, "BS-RoFormer") {
		t.Errorf("family = %q, want BS-RoFormer", bs.family)
	}
	// An empty upstream stem list must fall back to something displayable.
	if len(bs.stems) == 0 {
		t.Error("expected a fallback stem list when upstream reports none")
	}

	mel := byName["vocals_mel_band_roformer.ckpt"]
	if !strings.HasPrefix(mel.family, "Mel-Band RoFormer") {
		t.Errorf("family = %q, want Mel-Band RoFormer", mel.family)
	}
	if len(mel.stems) != 2 || mel.stems[0] != "vocals" || mel.stems[1] != "other" {
		t.Errorf("stems = %q, want the upstream list to be preserved", mel.stems)
	}
}

func TestParseModelListFallsBackOnBadJSON(t *testing.T) {
	// If the schema shifts, degrade to scraping names instead of returning
	// nothing.
	raw := `not json at all: mel_band_roformer_kim_ft2_unwa.ckpt and 1_HP-UVR.pth`
	got := parseModelList(raw)
	if len(got) != 1 || got[0].checkpoint != "mel_band_roformer_kim_ft2_unwa.ckpt" {
		t.Fatalf("fallback scan returned %v", got)
	}
}

func TestParseModelListLeadingNoise(t *testing.T) {
	got := parseModelList("WARNING: something\n" + sampleList)
	if len(got) != 2 {
		t.Errorf("leading log noise broke JSON parsing: got %d entries", len(got))
	}
}

func TestFamilyOf(t *testing.T) {
	tests := map[string]string{
		"bs_roformer_vocals_gabox.ckpt":       "BS-RoFormer",
		"BS-Roformer-SW.ckpt":                 "BS-RoFormer",
		"mel_band_roformer_kim_ft2_unwa.ckpt": "Mel-Band RoFormer",
		"melband_roformer_big_beta6x.ckpt":    "Mel-Band RoFormer",
		"something_else.ckpt":                 "RoFormer",
	}
	for in, want := range tests {
		if got := familyOf(in); got != want {
			t.Errorf("familyOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanLabel(t *testing.T) {
	if got := cleanLabel("Roformer Model: BS Roformer SW by jarredou"); got != "BS Roformer SW by jarredou" {
		t.Errorf("got %q", got)
	}
	if got := cleanLabel("Already clean"); got != "Already clean" {
		t.Errorf("got %q", got)
	}
}

func TestCuratedCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range curated {
		if seen[e.checkpoint] {
			t.Errorf("duplicate checkpoint %q", e.checkpoint)
		}
		seen[e.checkpoint] = true
		if !strings.HasSuffix(e.checkpoint, ".ckpt") {
			t.Errorf("%q should be a .ckpt filename", e.checkpoint)
		}
		if !strings.Contains(strings.ToLower(e.checkpoint), "roformer") {
			t.Errorf("%q does not look like a RoFormer model", e.checkpoint)
		}
		if e.label == "" || e.family == "" || e.note == "" || len(e.stems) == 0 {
			t.Errorf("%q has incomplete metadata", e.checkpoint)
		}
	}
}

func TestStemNameExtraction(t *testing.T) {
	// audio-separator names outputs "<input>_(Vocals)_<model>.<ext>".
	name := "Me at the zoo [jNQXAC9IVRw]_(vocals)_mel_band_roformer_kim_ft2_unwa.wav"
	m := stemNameRe.FindStringSubmatch(name)
	if m == nil || m[1] != "vocals" {
		t.Fatalf("failed to extract the stem name from %q: %v", name, m)
	}
}

func TestRankPutsVocalsFirst(t *testing.T) {
	if rank("vocals") >= rank("instrumental") {
		t.Error("vocals must sort before instrumental")
	}
	if rank("lead vocals") >= rank("other") {
		t.Error("lead vocals must sort before other stems")
	}
}

// The checkpoint name comes from a model index fetched over the network and is
// joined onto both the model dir and the output dir, so anything that is not a
// bare filename has to be refused.
func TestIsBareFilename(t *testing.T) {
	ok := []string{
		"bs_roformer_vocals_gabox.ckpt",
		"BS-Roformer-SW.ckpt",
		"mel_band_roformer_karaoke_aufr33_viperx_sdr_10.1956.ckpt",
	}
	for _, n := range ok {
		if !isBareFilename(n) {
			t.Errorf("isBareFilename(%q) = false, want true", n)
		}
	}

	bad := []string{
		"",
		".",
		"..",
		"../evil_roformer.ckpt",
		"../../../../home/user/.config/autostart/roformer.ckpt",
		"sub/dir/roformer.ckpt",
		`..\..\roformer.ckpt`,
		`C:\Windows\roformer.ckpt`,
		"/etc/roformer.ckpt",
		".hidden_roformer.ckpt",
	}
	for _, n := range bad {
		if isBareFilename(n) {
			t.Errorf("isBareFilename(%q) = true, want false", n)
		}
	}
}

func TestParseModelListRejectsTraversal(t *testing.T) {
	payload := `{
	  "MDXC": {
	    "Roformer Model: Evil": {
	      "filename": "../../../../home/user/.config/autostart/roformer.ckpt",
	      "stems": []
	    },
	    "Roformer Model: Good": {
	      "filename": "bs_roformer_vocals_gabox.ckpt",
	      "stems": []
	    }
	  }
	}`
	got := parseModelList(payload)
	if len(got) != 1 {
		t.Fatalf("expected only the safe entry, got %d: %v", len(got), got)
	}
	if got[0].checkpoint != "bs_roformer_vocals_gabox.ckpt" {
		t.Errorf("kept the wrong entry: %q", got[0].checkpoint)
	}
}

func TestFallbackScanRejectsTraversal(t *testing.T) {
	got := parseModelList("garbage ../evil_roformer.ckpt and mel_band_roformer_ok.ckpt")
	for _, e := range got {
		if !isBareFilename(e.checkpoint) {
			t.Errorf("fallback scan let %q through", e.checkpoint)
		}
	}
}

func TestRoformerModelsDoNotOfferStemChoice(t *testing.T) {
	b := New(func(context.Context) []string { return nil })
	models := b.Models(context.Background())
	if len(models) == 0 {
		t.Fatal("no models")
	}
	for _, m := range models {
		// audio-separator has no --two-stems equivalent; claiming otherwise puts
		// a live control in the UI that silently does nothing.
		if m.TwoStemsOption {
			t.Errorf("%s advertises a stem choice it cannot honour", m.Name)
		}
	}
}

// A CPU request is honoured by CUDA_VISIBLE_DEVICES= but not on Apple Silicon,
// where audio-separator reads torch.backends.mps.is_available() itself and
// Metal cannot be hidden. The caveat keys on the machine rather than on the
// app's probed GPU backend: that backend is only filled in after the probe
// passes, so keying on it would go silent on the broken-MPS Mac whose user
// picked CPU precisely to escape the crash.
func TestRoformerWarnsThatCPUIsIgnoredOnAppleSilicon(t *testing.T) {
	got := cpuCaveat("darwin", "arm64")
	if !strings.Contains(got, "MPS") || !strings.Contains(got, "Demucs") {
		t.Errorf("Apple Silicon phải cảnh báo CPU vẫn chạy MPS, được: %q", got)
	}
	// Everywhere else the request really is honoured, including an Intel Mac,
	// which has no MPS at all.
	for _, p := range [][2]string{
		{"linux", "amd64"}, {"windows", "amd64"}, {"darwin", "amd64"},
	} {
		if note := cpuCaveat(p[0], p[1]); note != "" {
			t.Errorf("%s/%s ép được CPU thật, không cần cảnh báo: %q", p[0], p[1], note)
		}
	}
}
