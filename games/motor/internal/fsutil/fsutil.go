// Package fsutil holds the two small file-system helpers the file-backed stores
// share, with the differences between Linux (production) and Windows
// (developer machines) in one place.
package fsutil

import (
	"os"
	"runtime"
	"time"
)

// SyncDir fsyncs a directory so renames and removals in it are durable. Windows
// cannot open a directory for syncing (it fails with "Access is denied"), and
// its file systems journal metadata themselves, so there the call does nothing.
func SyncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		return err
	}
	return d.Close()
}

// Windows replaces a file by rename only when nothing else has the target open;
// a reader, a virus scanner or the search indexer holding it for a moment makes
// the rename fail with a sharing violation or "Access is denied". The condition
// is transient, so on Windows the rename is retried for a short while.
const (
	renameRetries = 20
	renameBackoff = 15 * time.Millisecond
)

// RenameReplace moves src over dst, replacing dst if it exists. On Linux it is
// exactly os.Rename; on Windows it retries briefly (see above) before giving up.
func RenameReplace(src, dst string) error {
	err := os.Rename(src, dst)
	if runtime.GOOS != "windows" {
		return err
	}
	for i := 0; err != nil && i < renameRetries; i++ {
		time.Sleep(renameBackoff)
		err = os.Rename(src, dst)
	}
	return err
}
