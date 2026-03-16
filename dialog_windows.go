//go:build windows

package main

import (
	"os/exec"
	"strings"
	"syscall"
)

var hiddenProc = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}

func runPS(script string) (string, error) {
	cmd := exec.Command("powershell", "-NonInteractive", "-NoProfile", "-Command", script)
	cmd.SysProcAttr = hiddenProc
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// openFolderDialog opens a native Windows folder picker.
// A topmost owner Form ensures the dialog renders in front of the WebView2 window.
func openFolderDialog(title string) (string, error) {
	title = strings.ReplaceAll(title, "'", "''")
	script := `Add-Type -AssemblyName System.Windows.Forms; ` +
		`$owner = New-Object System.Windows.Forms.Form; ` +
		`$owner.TopMost = $true; $owner.ShowInTaskbar = $false; ` +
		`$owner.WindowState = 'Minimized'; $owner.Show(); $owner.Hide(); ` +
		`$d = New-Object System.Windows.Forms.FolderBrowserDialog; ` +
		`$d.Description = '` + title + `'; $d.ShowNewFolderButton = $true; ` +
		`if ($d.ShowDialog($owner) -eq 'OK') { Write-Output $d.SelectedPath } else { Write-Output '' }; ` +
		`$owner.Dispose()`
	return runPS(script)
}
