//go:build windows

package selfupdate

// Restart is not performed automatically on Windows.
//
// Windows has no execve, so restarting means starting a second process and
// letting this one exit — and the new process would race the old one for
// the listening port with no ordering guarantee. The honest options are a
// retry loop in the new process or a helper that waits for the old PID to
// die, and neither can be tested on the machine this is developed on.
//
// So the update installs, the process exits, and the user starts it again.
// Claiming an automatic restart that had never been run once would be
// worse than asking for a double-click.
func Restart(exePath string) error {
	return ErrManualRestart
}
