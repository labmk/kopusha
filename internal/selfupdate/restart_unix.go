//go:build !windows

package selfupdate

import (
	"fmt"
	"os"
	"syscall"
)

// Restart replaces this process with the newly installed binary, keeping
// the same arguments and environment.
//
// execve replaces the process image in place, so the PID, the terminal and
// anything holding the process are all preserved — a shell that launched
// kopusha keeps waiting on the same job. Nothing after this line runs.
func Restart(exePath string) error {
	if err := syscall.Exec(exePath, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("restart %s: %w", exePath, err)
	}
	return nil
}
