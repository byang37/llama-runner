//go:build !windows

package main

import "os/exec"

func assignToJob(_ *exec.Cmd) {}
