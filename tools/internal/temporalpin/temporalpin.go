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
// Package temporalpin reads the Temporal server version a go.mod requires, so a test can keep the engine's and the
// CLI's pins equal: in workspace mode a lower CLI pin changes the module graph the shipped engine is built from.
package temporalpin

import (
	"fmt"
	"regexp"
)

var requirement = regexp.MustCompile(`(?m)^\s*(?:require\s+)?go\.temporal\.io/server\s+(v\S+)`)

// Version returns the go.temporal.io/server version that the go.mod content requires.
func Version(gomod []byte) (string, error) {
	m := requirement.FindSubmatch(gomod)
	if m == nil {
		return "", fmt.Errorf("go.mod does not require go.temporal.io/server")
	}
	return string(m[1]), nil
}
