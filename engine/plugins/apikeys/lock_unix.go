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

//go:build unix

package apikeys

import (
	"os"
	"syscall"
)

func lockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_EX) } //nolint:gosec // a file descriptor

func lockShared(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_SH) } //nolint:gosec // a file descriptor

func unlockFile(f *os.File) error { return syscall.Flock(int(f.Fd()), syscall.LOCK_UN) } //nolint:gosec // a file descriptor

// keyTooOpen reports a key file its group can write or other users can use at all; the group may read it, as a
// Kubernetes secret mounted with fsGroup for a non-root user needs.
func keyTooOpen(info os.FileInfo) bool { return info.Mode().Perm()&0o027 != 0 }

// writableByOthers reports a file users outside its owner and group can write; a shared volume's group may.
func writableByOthers(info os.FileInfo) bool { return info.Mode().Perm()&0o002 != 0 }

// restrictToOwner does nothing on Unix, where files are created with mode 0600.
func restrictToOwner(string) error { return nil }
