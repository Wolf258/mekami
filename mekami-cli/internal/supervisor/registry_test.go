package supervisor

import (
	"testing"
)

func TestRegistry_UpsertFindRemove(t *testing.T) {
	r := &Registry{path: filepathJoinTemp(t), Version: 1}
	r.Upsert(DaemonState{Root: "/a", Lang: "go", DBPath: "/a/.mekami/graph.db"})
	r.Upsert(DaemonState{Root: "/b", Lang: "go", DBPath: "/b/.mekami/graph.db"})
	if got := r.Find("/a"); got == nil || got.Root != "/a" {
		t.Fatalf("find /a failed: %+v", got)
	}
	if got := r.Find("/missing"); got != nil {
		t.Fatalf("expected nil for missing, got %+v", got)
	}
	if !r.Remove("/a") {
		t.Fatalf("expected remove to return true")
	}
	if r.Find("/a") != nil {
		t.Fatalf("expected /a gone after remove")
	}
	if r.Remove("/a") {
		t.Fatalf("expected second remove to return false")
	}
}

func TestRegistry_SaveLoadRoundTrip(t *testing.T) {
	path := filepathJoinTemp(t)
	r, err := LoadRegistryAt(path)
	if err != nil {
		t.Fatal(err)
	}
	r.Upsert(DaemonState{Root: "/b", Lang: "go", DBPath: "/b/.mekami/graph.db", ConfigHash: "abc", RestartPolicy: "on-crash"})
	r.Upsert(DaemonState{Root: "/a", Lang: "go", DBPath: "/a/.mekami/graph.db", ConfigHash: "def", RestartPolicy: "always"})
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	r2, err := LoadRegistryAt(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(r2.Daemons) != 2 {
		t.Fatalf("expected 2 daemons, got %d", len(r2.Daemons))
	}
	// Save sorts by root for deterministic output.
	if r2.Daemons[0].Root != "/a" || r2.Daemons[1].Root != "/b" {
		t.Fatalf("daemons not sorted: %+v", r2.Daemons)
	}
	if r2.Daemons[0].ConfigHash != "def" {
		t.Fatalf("config hash lost: %+v", r2.Daemons[0])
	}
}

func TestRegistry_LoadMissing(t *testing.T) {
	r, err := LoadRegistryAt("/nonexistent/daemons.json")
	if err != nil {
		t.Fatalf("missing file should be ok, got %v", err)
	}
	if len(r.Daemons) != 0 {
		t.Fatalf("expected empty registry")
	}
}

func TestHashConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepathJoin(dir, "config.json")
	if _, err := writeFile(path, `{"version":1}`); err != nil {
		t.Fatal(err)
	}
	h1, err := HashConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == "" {
		t.Fatalf("expected non-empty hash")
	}
	// Same content -> same hash.
	if _, err := writeFile(path, `{"version":1}`); err != nil {
		t.Fatal(err)
	}
	h2, err := HashConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("expected stable hash, got %s vs %s", h1, h2)
	}
	// Different content -> different hash.
	if _, err := writeFile(path, `{"version":2}`); err != nil {
		t.Fatal(err)
	}
	h3, err := HashConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if h1 == h3 {
		t.Fatalf("expected different hash for different content")
	}
	// Missing file -> "" with no error.
	hMissing, err := HashConfig(filepathJoin(dir, "nope.json"))
	if err != nil {
		t.Fatalf("missing file should be ok: %v", err)
	}
	if hMissing != "" {
		t.Fatalf("expected empty hash for missing, got %q", hMissing)
	}
}
