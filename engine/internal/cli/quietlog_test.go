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

package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
	"go.temporal.io/server/service/history/shard"
)

type recordingLogger struct {
	log.Logger
	debug, errors []string
}

func (r *recordingLogger) Debug(msg string, _ ...tag.Tag) { r.debug = append(r.debug, msg) }
func (r *recordingLogger) Error(msg string, _ ...tag.Tag) { r.errors = append(r.errors, msg) }

func TestQuietLoggerDowngradesTransientStartupErrors(t *testing.T) {
	rec := &recordingLogger{Logger: log.NewNoopLogger()}
	clock := time.Now()
	q := newQuietLogger(rec).(quietLogger)
	q.now = func() time.Time { return clock }
	q.Error("Queue reader unable to retrieve tasks", tag.Error(shard.ErrShardStatusUnknown))
	q.Error("error fetching user data from parent", tag.Error(errors.New("Not enough hosts to serve the request")))
	q.Error("Unable to call matching.PollWorkflowTaskQueue.", tag.Error(errors.New("context canceled")))
	q.Error("Operation failed with internal error.", tag.Error(errors.New("SaveClusterMetadata encountered version mismatch")))
	q.Error("Persistence failure", tag.Error(errors.New("connection refused")))
	q.Error("No error tag")
	if len(rec.debug) != 4 || len(rec.errors) != 2 {
		t.Fatalf("during the grace period: debug = %v, errors = %v", rec.debug, rec.errors)
	}
	clock = clock.Add(startupGrace + time.Second)
	q.Error("Queue reader unable to retrieve tasks", tag.Error(shard.ErrShardStatusUnknown))
	if len(rec.errors) != 3 {
		t.Fatalf("after the grace period the error was not logged as an error: %v", rec.errors)
	}
}

func TestQuietLoggerKeepsTheCallersSourcePosition(t *testing.T) {
	file := filepath.Join(t.TempDir(), "engine.log")
	l := newQuietLogger(log.NewZapLogger(log.BuildZapLogger(log.Config{Level: "debug", OutputFile: file})))
	if _, ok := l.(quietLogger); !ok {
		t.Fatal("not a quiet logger")
	}
	_, _, line, _ := runtime.Caller(0)
	l.Info("info")
	l.Warn("warn")
	l.Debug("debug")
	l.Error("error")
	l.Error("transient", tag.Error(shard.ErrShardStatusUnknown))
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for entry := range strings.Lines(string(data)) {
		var e map[string]any
		if err := json.Unmarshal([]byte(entry), &e); err != nil {
			t.Fatalf("%v: %s", err, entry)
		}
		got = append(got, fmt.Sprintf("%s %s", e["msg"], filepath.Base(fmt.Sprint(e[tag.LoggingCallAtKey]))))
	}
	want := []string{}
	for i, msg := range []string{"info", "warn", "debug", "error", "transient"} {
		want = append(want, fmt.Sprintf("%s quietlog_test.go:%d", msg, line+1+i))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("logged at %v, want %v", got, want)
	}
}
