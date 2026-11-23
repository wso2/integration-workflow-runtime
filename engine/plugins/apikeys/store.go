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
	"bufio"
	"errors"
	"fmt"
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

// Revoke adds an ID to the revocation list at path; revoking a key twice is harmless.
func Revoke(path, id, reason string) error {
	if id == "" || strings.ContainsAny(id, " \t\n#") {
		return fmt.Errorf("%q is not an API key ID", id)
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

// appendLine adds a line, starting a new one when an earlier append was cut short, so a partial line never joins the
// next record: a revocation retried after a full disk must reach the list as its own line.
func appendLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, keyFileMode) //nolint:gosec // configured file
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	if size := info.Size(); size > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, size-1); err != nil {
			_ = f.Close()
			return err
		}
		if last[0] != '\n' {
			line = "\n" + line
		}
	}
	if _, err := f.WriteString(line); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
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
	f, err := os.Open(path) //nolint:gosec // the configured record
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var entries []Entry
	lines := bufio.NewScanner(f)
	// Room beyond any record Create allows, so a hand-edited line does not block list and revoke for every key.
	lines.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for n := 1; lines.Scan(); n++ {
		fields := strings.Split(lines.Text(), "\t")
		if len(fields) != 5 {
			return nil, fmt.Errorf("%s line %d: expected 5 tab-separated fields", path, n)
		}
		created, err1 := time.Parse(time.RFC3339, fields[3])
		expires, err2 := time.Parse(time.RFC3339, fields[4])
		if err := errors.Join(err1, err2); err != nil {
			return nil, fmt.Errorf("%s line %d: %w", path, n, err)
		}
		entries = append(entries, Entry{ID: fields[0], Name: fields[1], Grants: fields[2], Created: created,
			Expires: expires})
	}
	return entries, lines.Err()
}
