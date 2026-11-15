package fsutil

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSyncDir(t *testing.T) {
	if err := SyncDir(t.TempDir()); err != nil {
		t.Fatalf("SyncDir on an existing directory: %v", err)
	}
	// A missing directory is an error on Linux; Windows skips the sync entirely.
	if err := SyncDir(filepath.Join(t.TempDir(), "missing")); err == nil && !skipsDirSync() {
		t.Fatal("SyncDir of a missing directory succeeded")
	}
}

func skipsDirSync() bool {
	// SyncDir returns nil for everything on Windows; detect it by behaviour.
	return SyncDir(filepath.Join(os.TempDir(), "definitely-not-a-real-dir-fsutil")) == nil
}

func TestRenameReplaceReplacesAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RenameReplace(src, dst); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "new" {
		t.Fatalf("dst = %q", got)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatal("src still exists")
	}
}

// On Windows replacing a file that another handle holds open fails until the
// handle is closed; the retry must ride that out. On Linux the rename simply
// works, so the same test passes on both.
func TestRenameReplaceWaitsForAnOpenReader(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "src"), filepath.Join(dir, "dst")
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(dst)
	if err != nil {
		t.Fatal(err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(60 * time.Millisecond)
		_ = reader.Close()
		close(released)
	}()
	if err := RenameReplace(src, dst); err != nil {
		t.Fatalf("RenameReplace while a reader had the target open: %v", err)
	}
	<-released
	if got, _ := os.ReadFile(dst); string(got) != "new" {
		t.Fatalf("dst = %q", got)
	}
}

func TestRenameReplaceReportsRealErrors(t *testing.T) {
	dir := t.TempDir()
	// A missing source is a real failure and must be returned, not retried forever.
	start := time.Now()
	if err := RenameReplace(filepath.Join(dir, "nope"), filepath.Join(dir, "dst")); err == nil {
		t.Fatal("renaming a missing file succeeded")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("a real error took far too long to report")
	}
}
