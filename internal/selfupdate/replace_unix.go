//go:build !windows

package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
)

// replaceExecutable atomically swaps the running binary for bin. Writing the
// replacement into the same directory keeps the final rename on one filesystem,
// and on Unix renaming over a running executable is safe — the live process
// keeps its already-open inode.
func replaceExecutable(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".tarjan-upgrade-*")
	if err != nil {
		return fmt.Errorf("cannot write to %s — reinstall via install.sh or fix permissions: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(bin); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	// Flush to disk before the rename so a power loss right after the swap can't
	// leave a zero-length or partial binary at the executable path.
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, exe); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replacing %s — need write permission (try sudo) or reinstall via install.sh: %w", exe, err)
	}
	return nil
}
