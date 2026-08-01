//go:build !windows

package main

// Non-Windows build: no console-attach plumbing needed. The console
// subsystem distinction only exists on Windows; everywhere else the
// process inherits the parent's stdio normally.
func attachParentConsole() {}
