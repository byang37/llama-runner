//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// executableName returns the platform-specific llama-server binary name.
func executableName() string { return "llama-server.exe" }

// hideWindow configures a command to run without a visible console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
