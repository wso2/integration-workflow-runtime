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
	"fmt"
	"strings"
	"time"

	"go.temporal.io/server/common/log"
	"go.temporal.io/server/common/log/tag"
)

// startupGrace is how long after start the transient errors below are logged at debug level. While the services
// join the membership ring and acquire history shards, Temporal logs them at error level and retries; after the
// grace period they are logged as errors again, so a lasting problem still shows.
const startupGrace = 2 * time.Minute

// transientCauses are the error texts of those start-up conditions.
var transientCauses = []string{
	"Not enough hosts to serve the request",            // a service has not joined the membership ring yet
	"shard status unknown",                             // a history shard is still being acquired
	"context canceled",                                 // a call cut short while the ring settles
	"SaveClusterMetadata encountered version mismatch", // services racing to record the cluster on a first start
	"Frontend is not healthy yet",                      // a client checked health before the frontend was ready
}

// quietLogger logs transient start-up errors at debug level during the grace period, everything else unchanged.
type quietLogger struct {
	log.Logger
	until time.Time
	now   func() time.Time
}

func newQuietLogger(l log.Logger) log.Logger {
	if s, ok := l.(log.SkipLogger); ok {
		l = s.Skip(1)
	}
	return quietLogger{Logger: l, until: time.Now().Add(startupGrace), now: time.Now}
}

func (q quietLogger) Error(msg string, tags ...tag.Tag) {
	if q.now().Before(q.until) && transient(tags) {
		q.Debug(msg, tags...)
		return
	}
	q.Logger.Error(msg, tags...)
}

func transient(tags []tag.Tag) bool {
	text := errorText(tags)
	if text == "" {
		return false
	}
	for _, cause := range transientCauses {
		if strings.Contains(text, cause) {
			return true
		}
	}
	return false
}

// errorText is the text of the "error" tag, or empty.
func errorText(tags []tag.Tag) string {
	for _, t := range tags {
		if t.Key() == "error" {
			return fmt.Sprint(t.Value())
		}
	}
	return ""
}
