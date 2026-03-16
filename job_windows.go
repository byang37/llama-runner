//go:build windows

package main

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// jobHandle holds the Windows Job Object that owns all child processes.
// When this process exits (for any reason), the OS automatically terminates
// every process assigned to the job — including llama-server.
var jobHandle windows.Handle

func init() {
	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return
	}

	// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE: children are killed when the last
	// handle to the job is closed (i.e. when this process exits).
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		h,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		windows.CloseHandle(h)
		return
	}

	// Assign this process itself to the job so all subsequent child processes
	// it spawns are automatically added as well.
	self, err := windows.GetCurrentProcess()
	if err != nil {
		windows.CloseHandle(h)
		return
	}
	if err := windows.AssignProcessToJobObject(h, self); err != nil {
		// May fail if already in a job (e.g. under VS debugger or IDE).
		// Fall back to per-process assignment at spawn time.
		windows.CloseHandle(h)
		return
	}

	jobHandle = h
}

// assignToJob adds a freshly spawned cmd to the job object if the self-assign
// above failed (e.g. nested job scenario).
func assignToJob(cmd *exec.Cmd) {
	if jobHandle == 0 || cmd == nil || cmd.Process == nil {
		return
	}
	const processAllAccess = 0x1F0FFF
	h, err := windows.OpenProcess(
		processAllAccess, false, uint32(cmd.Process.Pid),
	)
	if err != nil {
		return
	}
	defer windows.CloseHandle(h)
	_ = windows.AssignProcessToJobObject(jobHandle, h)
}
