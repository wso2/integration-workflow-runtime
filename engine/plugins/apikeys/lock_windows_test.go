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
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"unsafe"
)

var (
	procGetNamedSecurityInfo = advapi32.NewProc("GetNamedSecurityInfoW")
	procConvertSDToString    = advapi32.NewProc("ConvertSecurityDescriptorToStringSecurityDescriptorW")
)

// TestCreatedFilesAreOwnerOnly checks that the signing key, the revocation list and the issued-key record get a
// protected ACL that grants only the current user and SYSTEM.
func TestCreatedFilesAreOwnerOnly(t *testing.T) {
	_, key := testKey(t)
	dir := filepath.Dir(key)
	list, record := filepath.Join(dir, "revoked"), filepath.Join(dir, "issued")
	if err := CreateList(list); err != nil {
		t.Fatal(err)
	}
	if err := Revoke(record, "0123456789abcdef0123456789abcdef", ""); err != nil {
		t.Fatal(err)
	}
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid, _ := user.User.Sid.String()
	for _, path := range []string{key, list, record} {
		got := daclOf(t, path)
		if !strings.HasPrefix(got, "D:P") || strings.Count(got, "(A;") != 2 || !strings.Contains(got, ";;;"+sid+")") ||
			!strings.Contains(got, ";;;SY)") {
			t.Errorf("%s has ACL %s; want a protected ACL for %s and SYSTEM only", path, got, sid)
		}
	}
}

func daclOf(t *testing.T, path string) string {
	t.Helper()
	name, _ := syscall.UTF16PtrFromString(path)
	var sd uintptr
	if r, _, _ := procGetNamedSecurityInfo.Call(uintptr(unsafe.Pointer(name)), seFileObject, daclSecurityInformation,
		0, 0, 0, 0, uintptr(unsafe.Pointer(&sd))); r != 0 {
		t.Fatalf("GetNamedSecurityInfo(%s) = %v", path, syscall.Errno(r))
	}
	defer procLocalFree.Call(sd) //nolint:errcheck // test cleanup
	var text *uint16
	if r, _, err := procConvertSDToString.Call(sd, sddlRevision1, daclSecurityInformation,
		uintptr(unsafe.Pointer(&text)), 0); r == 0 {
		t.Fatalf("ConvertSecurityDescriptorToStringSecurityDescriptor = %v", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(text))) //nolint:errcheck // test cleanup
	n := 0
	for p := unsafe.Pointer(text); *(*uint16)(p) != 0; p = unsafe.Add(p, 2) {
		n++
	}
	return syscall.UTF16ToString(unsafe.Slice(text, n))
}
