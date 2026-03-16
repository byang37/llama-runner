//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// openFolderDialog opens a native folder picker on macOS (osascript) and
// Linux (zenity / kdialog). Falls back to stdin prompt if no GUI tool is found.
func openFolderDialog(title string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(
			`tell application "Finder" to set f to choose folder with prompt %q`,
			title,
		)
		out, err := exec.Command("osascript", "-e", script).Output()
		if err != nil {
			return "", err
		}
		// osascript returns "alias Macintosh HD:Users:..." — convert to POSIX
		alias := strings.TrimSpace(string(out))
		posix, err := exec.Command("osascript", "-e",
			fmt.Sprintf(`tell application "Finder" to POSIX path of (%q as alias)`, alias),
		).Output()
		if err != nil {
			return strings.TrimRight(alias, "\n"), nil
		}
		return strings.TrimRight(string(posix), "\n"), nil

	default: // Linux and others
		// Try zenity (GTK), then kdialog (Qt), then fall back to terminal prompt.
		if _, err := exec.LookPath("zenity"); err == nil {
			out, err := exec.Command("zenity", "--file-selection",
				"--directory", "--title="+title).Output()
			if err != nil {
				return "", nil // user cancelled
			}
			return strings.TrimSpace(string(out)), nil
		}
		if _, err := exec.LookPath("kdialog"); err == nil {
			out, err := exec.Command("kdialog", "--getexistingdirectory",
				".", "--title", title).Output()
			if err != nil {
				return "", nil
			}
			return strings.TrimSpace(string(out)), nil
		}
		return "", fmt.Errorf("no GUI folder picker found; install zenity or kdialog")
	}
}
