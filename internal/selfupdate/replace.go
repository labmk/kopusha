package selfupdate

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func isWindows() bool { return runtime.GOOS == "windows" }

// backupSuffix marks the outgoing binary. It is kept rather than deleted
// so a failed update can be undone by renaming one file, and on Windows
// it cannot be deleted immediately anyway — the running process still
// holds it.
const backupSuffix = ".old"

// replaceBinary swaps the executable at exePath for data, leaving the
// previous binary alongside it as <exePath>.old.
//
// The same three steps work on every platform, which is worth stating
// because the platform differences here are usually overstated:
//
//	write <exe>.new → rename <exe> to <exe>.old → rename <exe>.new to <exe>
//
// Unix can unlink or rename a running executable freely. Windows cannot
// *delete* or overwrite a running image, but it can rename it, which is
// all step two needs. The genuine difference is only cleanup: the backup
// can be removed straight away on Unix, whereas on Windows it stays
// locked until the process exits and is swept up by the next launch.
//
// Both renames are within one directory, so neither crosses a filesystem
// boundary and neither can half-complete.
func replaceBinary(exePath string, data []byte) (backup string, err error) {
	dir := filepath.Dir(exePath)

	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exePath); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".kopusha-new-*")
	if err != nil {
		return "", fmt.Errorf("stage new binary: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write new binary: %w", err)
	}
	// Both must succeed before the swap: a short write or an unflushed
	// buffer would install a truncated executable.
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("flush new binary: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return "", fmt.Errorf("close new binary: %w", err)
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return "", fmt.Errorf("set mode on new binary: %w", err)
	}

	backup = exePath + backupSuffix
	// A backup from a previous update would block the rename on Windows.
	os.Remove(backup)

	if err = os.Rename(exePath, backup); err != nil {
		return "", fmt.Errorf("move the running binary aside: %w", err)
	}
	if err = os.Rename(tmpName, exePath); err != nil {
		// Put the original back rather than leaving no executable at all.
		if rbErr := os.Rename(backup, exePath); rbErr != nil {
			return "", fmt.Errorf("install new binary: %w (and the previous binary is now at %s)", err, backup)
		}
		return "", fmt.Errorf("install new binary: %w", err)
	}
	return backup, nil
}

// Rollback restores the previous binary. Kept as a named operation rather
// than left to the caller so that the reverse of an update is one step
// and cannot be got subtly wrong under pressure.
func Rollback(exePath string) error {
	backup := exePath + backupSuffix
	if _, err := os.Stat(backup); err != nil {
		return fmt.Errorf("no previous binary at %s", backup)
	}
	if err := os.Remove(exePath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove current binary: %w", err)
	}
	return os.Rename(backup, exePath)
}

// SweepBackup removes the binary left behind by a previous update. Call it
// at startup: on Windows the file is locked until the old process exits,
// so the next launch is the first moment it can go.
//
// Failure is deliberately silent to the caller's log level of choice — a
// leftover file is untidy, not broken, and an install directory that is
// no longer writable must not turn every start into a warning.
func SweepBackup(exePath string) (removed bool) {
	backup := exePath + backupSuffix
	if _, err := os.Stat(backup); err != nil {
		return false
	}
	return os.Remove(backup) == nil
}
