//go:build !windows

package main

import "os/exec"

// executableName returns the platform-specific llama-server binary name.
func executableName() string { return "llama-server" }

// hideWindow is a no-op on Unix: there is no GUI console window to hide.
func hideWindow(_ *exec.Cmd) {}
