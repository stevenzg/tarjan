//go:build windows

package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
)

// replaceExecutable swaps the running binary for bin. Windows will not let a
// running .exe be overwritten, so the live binary is renamed aside first; the
// leftover .old is cleaned up best-effort (it may linger until the process
// exits, which is harmless).
func replaceExecutable(exe string, bin []byte) error {
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, "tarjan-upgrade-*.exe")
	if err != nil {
		return fmt.Errorf("cannot write to %s — reinstall via install.ps1 or fix permissions: %w", dir, err)
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

	old := exe + ".old"
	_ = os.Remove(old)
	if err := os.Rename(exe, old); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("replacing %s — need write permission or reinstall via install.ps1: %w", exe, err)
	}
	if err := os.Rename(tmpName, exe); err != nil {
		_ = os.Rename(old, exe) // roll back
		_ = os.Remove(tmpName)
		return fmt.Errorf("replacing %s: %w", exe, err)
	}
	_ = os.Remove(old) // best effort; typically removable only after exit
	return nil
}
