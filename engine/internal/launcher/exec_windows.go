// Copyright (c) 2026, WSO2 LLC. (http://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

//go:build windows

package launcher

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"
)

// execEngine runs the engine as a child sharing the console and returns its exit code. The console delivers Ctrl+C
// to both; the launcher keeps waiting while the engine shuts down, and a job object ends the engine with the launcher.
func execEngine(engine string, args []string) (int, error) {
	interrupts := make(chan os.Signal, 1)
	// A handled interrupt keeps Go's default from ending the launcher before the engine exits.
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	cmd := exec.Command(engine, args...) //nolint:gosec // the runtime's own engine
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	err := cmd.Start()
	if err == nil {
		if job, jobErr := killWithLauncher(cmd.Process.Pid); jobErr == nil {
			defer syscall.CloseHandle(job) //nolint:errcheck // the engine has exited
		}
		err = cmd.Wait()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, err
	}
	return 0, nil
}

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObject         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJob      = kernel32.NewProc("AssignProcessToJobObject")
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x2000
	processSetQuota                   = 0x0100
	processTerminate                  = 0x0001
)

// jobObjectExtendedLimit is JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobObjectExtendedLimit struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
	IoCounters              [6]uint64
	ProcessMemoryLimit      uintptr
	JobMemoryLimit          uintptr
	PeakProcessMemoryUsed   uintptr
	PeakJobMemoryUsed       uintptr
}

// killWithLauncher puts the process in a job that Windows ends when its last handle closes: when the launcher exits,
// even if it is killed without its child.
func killWithLauncher(pid int) (syscall.Handle, error) {
	r, _, err := procCreateJobObject.Call(0, 0)
	if r == 0 {
		return 0, err
	}
	job := syscall.Handle(r)
	info := jobObjectExtendedLimit{LimitFlags: jobObjectLimitKillOnJobClose}
	r, _, err = procSetInformationJobObject.Call(uintptr(job), jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info))
	if r == 0 {
		_ = syscall.CloseHandle(job)
		return 0, err
	}
	process, err := syscall.OpenProcess(processSetQuota|processTerminate, false, uint32(pid)) //nolint:gosec // a pid
	if err != nil {
		_ = syscall.CloseHandle(job)
		return 0, err
	}
	defer syscall.CloseHandle(process) //nolint:errcheck // only needed for the assignment
	if r, _, err = procAssignProcessToJob.Call(uintptr(job), uintptr(process)); r == 0 {
		_ = syscall.CloseHandle(job)
		return 0, err
	}
	return job, nil
}
