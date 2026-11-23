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
//
// queueName follows GetHistoryTaskQueueName and combineUnique in Temporal's common/persistence (v1.32.1):
// Copyright (c) 2025 Temporal Technologies Inc.  All rights reserved.
// Copyright (c) 2020 Uber Technologies, Inc.
// Used under the MIT License; see third-party/licenses/temporal-server-LICENSE.

package command

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// queueName is the engine's name for a history task queue, as Temporal's persistence.GetHistoryTaskQueueName builds
// it: the category, both clusters and a hash of the clusters, which keeps clusters with underscores apart.
func queueName(category int32, source, target string) string {
	h := sha256.New()
	for _, cluster := range []string{source, target} {
		sum := sha256.Sum256([]byte(cluster))
		_, _ = h.Write(sum[:])
	}
	return fmt.Sprintf("%d_%s_%s_%s", category, source, target, base64.StdEncoding.EncodeToString(h.Sum(nil))[:8])
}

// parseQueueName finds the category and clusters of a queue name, trying each underscore between the clusters
// against the name's hash, since a cluster name can hold underscores.
func parseQueueName(name string) (category int32, source, target string, ok bool) {
	first, last := strings.Index(name, "_"), strings.LastIndex(name, "_")
	if first < 0 || last <= first {
		return 0, "", "", false
	}
	id, err := strconv.ParseInt(name[:first], 10, 32)
	if err != nil {
		return 0, "", "", false
	}
	clusters := name[first+1 : last]
	for i := 0; i < len(clusters); i++ {
		if clusters[i] == '_' && queueName(int32(id), clusters[:i], clusters[i+1:]) == name {
			return int32(id), clusters[:i], clusters[i+1:], true
		}
	}
	return 0, "", "", false
}
