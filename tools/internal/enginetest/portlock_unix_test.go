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

package enginetest

import (
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

var portLock struct {
	sync.Mutex
	file *os.File
}

// lockPorts takes the host-wide lock that freeBase and start hold until an engine reports SERVING. The operating
// system drops it when the process ends, so a killed run never leaves it behind. Within this process it is shared.
func lockPorts(t *testing.T) {
	t.Helper()
	portLock.Lock()
	defer portLock.Unlock()
	if portLock.file != nil {
		return
	}
	lock := filepath.Join(os.TempDir(), "workflow-enginetest-ports.lock") //nolint:usetesting // shared by every run
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	portLock.file = f
	t.Cleanup(unlockPorts)
}

func unlockPorts() {
	portLock.Lock()
	defer portLock.Unlock()
	if portLock.file != nil {
		_ = portLock.file.Close()
		portLock.file = nil
	}
}
