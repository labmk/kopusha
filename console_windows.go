//go:build windows

package main

import (
	"os"
	"syscall"
)

// attachParentConsole tries to attach to the console of the parent
// process. When the binary is linked with `-H windowsgui` (no console
// subsystem) and launched from Explorer or a task scheduler, there is
// no console — AttachConsole fails and the process runs silently. When
// the same binary is launched from cmd.exe / PowerShell, AttachConsole
// succeeds and we re-bind os.Stdout / os.Stderr to the parent's
// CONOUT$ so `--version`, `--help`, and any log.Print* still produce
// visible output in the caller's terminal.
//
// This is the standard Windows pattern for binaries that want to be
// both a desktop app (no flashing console window) and a usable CLI.
// Called from main.go before flag.Parse so version/help output works.
func attachParentConsole() {
	const ATTACH_PARENT_PROCESS = ^uintptr(0)
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	attachConsole := kernel32.NewProc("AttachConsole")
	r, _, _ := attachConsole.Call(ATTACH_PARENT_PROCESS)
	if r == 0 {
		// No parent console (Explorer launch, double-click, etc.). Run silently.
		return
	}
	// Re-open the standard handles against the now-attached console. The
	// original os.Stdout/os.Stderr point at invalid file descriptors
	// because the GUI-subsystem binary started without any.
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stdout = f
	}
	if f, err := os.OpenFile("CONOUT$", os.O_WRONLY, 0); err == nil {
		os.Stderr = f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDONLY, 0); err == nil {
		os.Stdin = f
	}
}
