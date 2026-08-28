package demucs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBagModels(t *testing.T) {
	// The real htdemucs_ft.yaml shape, including the weights matrix that must
	// not confuse the parser.
	yaml := `models: ['f7e0c4bc', 'd12395a8', '92cfc3b6', '04573f0d']
weights: [
  [1., 0., 0., 0.],
  [0., 1., 0., 0.],
]`
	got := bagModels(yaml)
	want := []string{"f7e0c4bc", "d12395a8", "92cfc3b6", "04573f0d"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("model %d = %q, want %q", i, got[i], want[i])
		}
	}

	if got := bagModels("models: ['955717e8']"); len(got) != 1 || got[0] != "955717e8" {
		t.Errorf("single-model bag: got %q", got)
	}
	if got := bagModels("segment: 44"); got != nil {
		t.Errorf("expected nil for a yaml without models, got %q", got)
	}
}

func TestRemoteIndex(t *testing.T) {
	index, err := remoteIndex()
	if err != nil {
		t.Fatalf("remoteIndex: %v", err)
	}
	// files.txt groups entries under "root:" lines; the signature must resolve
	// to a URL carrying the right subdirectory.
	url, ok := index["f7e0c4bc"]
	if !ok {
		t.Fatal("htdemucs_ft signature f7e0c4bc missing from the index")
	}
	const want = "https://dl.fbaipublicfiles.com/demucs/hybrid_transformer/f7e0c4bc-ba3fe64a.th"
	if url != want {
		t.Errorf("url = %q, want %q", url, want)
	}
	if mdx, ok := index["e51eebcc"]; !ok || !strings.Contains(mdx, "/mdx_final/") {
		t.Errorf("mdx entry should sit under mdx_final/, got %q", mdx)
	}
}

func TestFilesFor(t *testing.T) {
	files, err := filesFor("htdemucs_ft")
	if err != nil {
		t.Fatalf("filesFor: %v", err)
	}
	if len(files) != 4 {
		t.Fatalf("htdemucs_ft should be a bag of 4, got %d", len(files))
	}
	for _, f := range files {
		if !strings.HasSuffix(f.name, ".th") {
			t.Errorf("unexpected filename %q", f.name)
		}
		// The checksum is parsed out of the filename; without it we would be
		// downloading unverified weights and then unpickling them.
		if len(f.checksum) != 8 {
			t.Errorf("file %q: checksum = %q, want 8 hex chars", f.name, f.checksum)
		}
		if !strings.HasPrefix(f.url, "https://") {
			t.Errorf("file %q: url = %q", f.name, f.url)
		}
		if f.size <= 0 {
			t.Errorf("file %q: size = %d", f.name, f.size)
		}
	}

	if _, err := filesFor("htdemucs"); err != nil {
		t.Errorf("filesFor(htdemucs): %v", err)
	}
	if _, err := filesFor("htdemucs_6s"); err != nil {
		t.Errorf("filesFor(htdemucs_6s): %v", err)
	}
	if _, err := filesFor("no-such-model"); err == nil {
		t.Error("expected an error for an unknown model")
	}
}

// Every catalog entry must be resolvable, or the UI would offer a model that
// cannot be downloaded.
func TestCatalogIsResolvable(t *testing.T) {
	for _, s := range catalog {
		files, err := filesFor(s.name)
		if err != nil {
			t.Errorf("catalog entry %q: %v", s.name, err)
			continue
		}
		if len(files) == 0 {
			t.Errorf("catalog entry %q resolves to no files", s.name)
		}
		if s.bytes <= 0 {
			t.Errorf("catalog entry %q has no size", s.name)
		}
		if len(s.stems) == 0 {
			t.Errorf("catalog entry %q lists no stems", s.name)
		}
	}
}

func TestSeparationEnvForcesLegacyTorchLoad(t *testing.T) {
	// demucs 4.0.1 checkpoints are pickled objects, so they cannot load under
	// torch >= 2.6 defaults. Losing this variable silently breaks every run.
	var found bool
	for _, kv := range separationEnv() {
		if kv == "TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1" {
			found = true
		}
	}
	if !found {
		t.Error("separationEnv must set TORCH_FORCE_NO_WEIGHTS_ONLY_LOAD=1")
	}
}

func TestRankPutsVocalsFirst(t *testing.T) {
	if rank("vocals") >= rank("no_vocals") {
		t.Error("vocals must sort before no_vocals")
	}
	if rank("no_vocals") >= rank("drums") {
		t.Error("no_vocals must sort before the remaining stems")
	}
}

// writeCheckpoint creates a checkpoint file and returns its path.
func writeCheckpoint(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Models() is asked constantly by the UI, so verification must be hashed once
// per file version, not once per call (~476 MB per call with all three models).
func TestCheckpointOKCachesByStamp(t *testing.T) {
	dir := t.TempDir()
	// sha256("hello") starts with 2cf24dba.
	p := writeCheckpoint(t, dir, "aaaaaaaa-2cf24dba.th", "hello")

	b := New(func(context.Context) []string { return nil })
	if !b.checkpointOK(p, "2cf24dba") {
		t.Fatal("first check should pass")
	}

	b.verifiedMu.Lock()
	stamped, ok := b.verified[p]
	b.verifiedMu.Unlock()
	if !ok {
		t.Fatal("a passing check must be remembered")
	}
	if stamped.size != 5 {
		t.Errorf("stamp size = %d, want 5", stamped.size)
	}

	// Repeat calls must be answered from the stamp.
	for i := 0; i < 5; i++ {
		if !b.checkpointOK(p, "2cf24dba") {
			t.Fatalf("cached check %d failed", i)
		}
	}

	// Corrupting the file changes size and mtime, so the stamp must not shield it.
	if err := os.WriteFile(p, []byte("tampered"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b.checkpointOK(p, "2cf24dba") {
		t.Error("a replaced file must be re-verified and rejected")
	}
}

func TestCheckpointOKRejectsMissingAndEmpty(t *testing.T) {
	dir := t.TempDir()
	b := New(func(context.Context) []string { return nil })

	if b.checkpointOK(filepath.Join(dir, "nope.th"), "2cf24dba") {
		t.Error("missing file accepted")
	}
	empty := writeCheckpoint(t, dir, "empty.th", "")
	if b.checkpointOK(empty, "2cf24dba") {
		t.Error("empty file accepted")
	}
	if b.checkpointOK(dir, "2cf24dba") {
		t.Error("directory accepted")
	}
}

func TestMarkVerifiedSkipsRehash(t *testing.T) {
	dir := t.TempDir()
	// Deliberately the wrong digest: if markVerified works, checkpointOK trusts
	// the stamp and never notices.
	p := writeCheckpoint(t, dir, "bbbbbbbb-deadbeef.th", "hello")
	b := New(func(context.Context) []string { return nil })
	b.markVerified(p)
	if !b.checkpointOK(p, "deadbeef") {
		t.Error("markVerified should let the next check short-circuit")
	}
}

func TestDemucsModelsOfferStemChoice(t *testing.T) {
	b := New(func(context.Context) []string { return nil })
	for _, m := range b.Models(context.Background()) {
		if !m.TwoStemsOption {
			t.Errorf("%s should honour the stem choice (demucs takes --two-stems)", m.Name)
		}
	}
}
