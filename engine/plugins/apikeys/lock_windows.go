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

package apikeys

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procLockFileEx   = kernel32.NewProc("LockFileEx")
	procUnlockFileEx = kernel32.NewProc("UnlockFileEx")
	procLocalFree    = kernel32.NewProc("LocalFree")

	advapi32                      = syscall.NewLazyDLL("advapi32.dll")
	procConvertStringSDToSD       = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procGetSecurityDescriptorDacl = advapi32.NewProc("GetSecurityDescriptorDacl")
	procSetNamedSecurityInfo      = advapi32.NewProc("SetNamedSecurityInfoW")
)

const (
	seFileObject                     = 1
	daclSecurityInformation          = 0x4
	protectedDaclSecurityInformation = 0x80000000
	sddlRevision1                    = 1
)

const lockfileExclusiveLock = 0x2

// lockFile takes LockFileEx's exclusive lock on the whole file, waiting for any other holder.
func lockFile(f *os.File) error {
	var o syscall.Overlapped
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileExclusiveLock, 0, 0xFFFFFFFF, 0xFFFFFFFF,
		uintptr(unsafe.Pointer(&o)))
	if r == 0 {
		return err
	}
	return nil
}

// lockShared takes LockFileEx's shared lock, which readers hold together while no writer does.
func lockShared(f *os.File) error {
	var o syscall.Overlapped
	r, _, err := procLockFileEx.Call(f.Fd(), 0, 0, 0xFFFFFFFF, 0xFFFFFFFF, uintptr(unsafe.Pointer(&o)))
	if r == 0 {
		return err
	}
	return nil
}

func unlockFile(f *os.File) error {
	var o syscall.Overlapped
	r, _, err := procUnlockFileEx.Call(f.Fd(), 0, 0xFFFFFFFF, 0xFFFFFFFF, uintptr(unsafe.Pointer(&o)))
	if r == 0 {
		return err
	}
	return nil
}

// keyTooOpen is false on Windows, whose file access is controlled by ACLs rather than mode bits; restrictToOwner
// sets the ACL of the files the runtime creates.
func keyTooOpen(os.FileInfo) bool { return false }

// writableByOthers is false on Windows, as keyTooOpen.
func writableByOthers(os.FileInfo) bool { return false }

// restrictToOwner replaces the file's inherited ACL with one that grants full access to the current user and to
// SYSTEM only. SYSTEM runs Windows services and already controls the machine, so it gains nothing it lacked.
func restrictToOwner(path string) error {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	sid, err := user.User.Sid.String()
	if err != nil {
		return err
	}
	// "P" protects the ACL from inherited entries; FA is full access; SY is SYSTEM.
	sddl, err := syscall.UTF16PtrFromString("D:P(A;;FA;;;" + sid + ")(A;;FA;;;SY)")
	if err != nil {
		return err
	}
	var sd uintptr
	if r, _, err := procConvertStringSDToSD.Call(uintptr(unsafe.Pointer(sddl)), sddlRevision1,
		uintptr(unsafe.Pointer(&sd)), 0); r == 0 {
		return err
	}
	defer procLocalFree.Call(sd) //nolint:errcheck // freeing the descriptor cannot fail usefully
	var present, defaulted int32
	var dacl uintptr
	if r, _, err := procGetSecurityDescriptorDacl.Call(sd, uintptr(unsafe.Pointer(&present)),
		uintptr(unsafe.Pointer(&dacl)), uintptr(unsafe.Pointer(&defaulted))); r == 0 {
		return err
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if r, _, _ := procSetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), seFileObject,
		daclSecurityInformation|protectedDaclSecurityInformation, 0, 0, dacl, 0); r != 0 {
		return syscall.Errno(r)
	}
	return nil
}
