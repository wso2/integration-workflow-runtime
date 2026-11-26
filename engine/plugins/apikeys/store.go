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

package apikeys

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// The issued-key record is one tab-separated line per key: ID, name, grants, created, expires. It holds no token, so
// a leaked record grants nothing; it exists so operators can list keys and revoke them by name.

// Record appends an issued key to the record at path.
func Record(path string, k Issued) error {
	grants := make([]string, len(k.Grants))
	for i, g := range k.Grants {
		grants[i] = g.Namespace + ":" + strings.ToLower(g.Role)
	}
	line := strings.Join([]string{k.ID, k.Name, strings.Join(grants, ","), k.Created.Format(time.RFC3339),
		k.Expires.Format(time.RFC3339)}, "\t") + "\n"
	return appendLine(path, line)
}

// idLength is the length of a key ID: 16 random bytes in lowercase hex.
const idLength = 32

// ValidID reports whether id has the form of the IDs the runtime issues.
func ValidID(id string) bool {
	if len(id) != idLength {
		return false
	}
	for _, c := range id {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Revoke adds an ID to the revocation list at path; revoking a key twice is harmless.
func Revoke(path, id, reason string) error {
	if !ValidID(id) {
		return fmt.Errorf("%q is not an API key ID: an ID is the %d hex characters in the ID column of api-key list, "+
			"not the key itself", id, idLength)
	}
	if len(reason) > maxReasonLength {
		return fmt.Errorf("the reason is longer than %d characters", maxReasonLength)
	}
	line := id
	if reason = sanitize(reason); reason != "" {
		line += " # " + reason
	}
	return appendLine(path, line+"\n")
}

// appendLine adds a complete line. It holds an exclusive lock, so two api-key commands never interleave, and first
// removes any line an earlier append left without its end: a write cut short by a full disk is dropped rather than
// joined to the next record or left for the readers.
func appendLine(path, line string) (err error) {
	f, err := openPrivate(path, os.O_RDWR)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := f.Close(); err == nil {
			err = closeErr
		}
	}()
	if err := lockFile(f); err != nil {
		return fmt.Errorf("unable to lock %s: %w", path, err)
	}
	defer func() { _ = unlockFile(f) }()
	data, err := io.ReadAll(f)
	if err != nil {
		return err
	}
	end := int64(len(data))
	if end > 0 && data[end-1] != '\n' {
		end = int64(bytes.LastIndexByte(data, '\n') + 1)
		if err := f.Truncate(end); err != nil {
			return err
		}
	}
	if _, err := f.WriteAt([]byte(line), end); err != nil {
		return err
	}
	return f.Sync()
}

// readLines reads a file's lines under a shared lock, so it never sees an append half done. A last line without its
// newline was cut short; keepPartial keeps it (a revocation list must not drop a hand-written last ID).
func readLines(path string, keepPartial bool) (lines []string, err error) {
	f, err := os.Open(path) //nolint:gosec // a configured file
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if err := lockShared(f); err != nil {
		return nil, fmt.Errorf("unable to lock %s: %w", path, err)
	}
	defer func() { _ = unlockFile(f) }()
	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	if i := bytes.LastIndexByte(data, '\n'); !keepPartial && i < len(data)-1 {
		data = data[:i+1]
	}
	text := strings.TrimSuffix(string(data), "\n")
	if text == "" {
		return nil, nil
	}
	return strings.Split(text, "\n"), nil
}

// CreateList makes an empty revocation list when there is none, so a list missing later means it was not copied.
// An existing list keeps its mode: the verifier refuses one that other users can write.
func CreateList(path string) error {
	f, err := openPrivate(path, os.O_WRONLY)
	if err != nil {
		return err
	}
	return f.Close()
}

// openPrivate opens path with flag, creating it first if needed with access for its owner only.
func openPrivate(path string, flag int) (*os.File, error) {
	f, err := os.OpenFile(path, flag|os.O_CREATE|os.O_EXCL, keyFileMode) //nolint:gosec // a configured file
	if errors.Is(err, os.ErrExist) {
		return os.OpenFile(path, flag, keyFileMode) //nolint:gosec // a configured file
	}
	if err != nil {
		return nil, err
	}
	if err := restrictToOwner(path); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("unable to restrict access to %s: %w", path, err)
	}
	return f, nil
}

// maxReasonLength keeps every revocation line far below what ReadRevoked reads; an unreadable list would refuse
// every API key.
const maxReasonLength = 1000

func sanitize(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "#", "")), " ")
}

// Entry is one issued key in the record.
type Entry struct {
	ID      string
	Name    string
	Grants  string
	Created time.Time
	Expires time.Time
}

// ReadRecord reads the issued-key record; a missing file has no keys.
func ReadRecord(path string) ([]Entry, error) {
	lines, err := readLines(path, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var entries []Entry
	for n, line := range lines {
		fields := strings.Split(line, "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("%s line %d: expected 5 tab-separated fields", path, n+1)
		}
		created, err1 := time.Parse(time.RFC3339, fields[3])
		expires, err2 := time.Parse(time.RFC3339, fields[4])
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n+1, err)
		}
		entries = append(entries, Entry{ID: fields[0], Name: fields[1], Grants: fields[2], Created: created,
			Expires: expires})
	}
	return entries, nil
}
