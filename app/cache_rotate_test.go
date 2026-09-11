package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seed writes a cache file of size bytes with the given age.
func seed(t *testing.T, dir, name string, size int, age time.Duration, now time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, make([]byte, size), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, now.Add(-age), now.Add(-age)); err != nil {
		t.Fatal(err)
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestRotateCacheDropsExpiredFiles(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	seed(t, dir, "old", 10, 2*time.Hour, now)
	seed(t, dir, "new", 10, time.Minute, now)

	if err := rotateCache(dir, time.Hour, 0, now); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "new" {
		t.Errorf("files after rotation: %v, want only new", got)
	}
}

// TestRotateCacheTrimsOldestToTheCap is the regression test for #36: over the
// cap the oldest files go until the rest fit, and the directory itself is
// never removed.
func TestRotateCacheTrimsOldestToTheCap(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	seed(t, dir, "oldest", 100, 3*time.Hour, now)
	seed(t, dir, "middle", 100, 2*time.Hour, now)
	seed(t, dir, "newest", 100, time.Hour, now)

	if err := rotateCache(dir, 0, 250, now); err != nil {
		t.Fatal(err)
	}
	got := names(t, dir)
	if len(got) != 2 || got[0] != "middle" || got[1] != "newest" {
		t.Errorf("files after rotation: %v, want middle and newest", got)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("cache directory removed: %v", err)
	}
}

func TestRotateCacheExpiredFilesDoNotCountTowardTheCap(t *testing.T) {
	dir, now := t.TempDir(), time.Now()
	seed(t, dir, "expired", 200, 2*time.Hour, now)
	seed(t, dir, "kept", 100, time.Minute, now)

	if err := rotateCache(dir, time.Hour, 150, now); err != nil {
		t.Fatal(err)
	}
	if got := names(t, dir); len(got) != 1 || got[0] != "kept" {
		t.Errorf("files after rotation: %v, want only kept (the expired file must not push it over the cap)", got)
	}
}

func TestRotateCacheCreatesAMissingDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	if err := rotateCache(dir, 0, 0, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory not created: %v", err)
	}
}
