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

package runtimeconfig

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// DevOptions configure a development engine: every service, SQLite, no runtime.toml.
type DevOptions struct {
	// IP the engine listens on and advertises; 127.0.0.1 keeps it on this machine.
	IP string
	// Port of the frontend; the other services use the default offsets from it (7233: 7234-7243).
	Port int
	// DBFile keeps the data in this SQLite file; empty keeps it in memory, lost on exit.
	DBFile string
	// LogLevel is debug, info, warn or error.
	LogLevel string
}

// ClusterName is the name of the engine's single cluster, which namespaces created before start must name.
const ClusterName = clusterName

// Dev returns the configuration of a development engine, validated like a runtime.toml.
func Dev(o DevOptions) (*Runtime, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "[engine]\nbindAddress = %q\nlogLevel = %q\n", o.IP, o.LogLevel)
	fmt.Fprintf(&b, "[engine.ports]\nfrontend = %d\nfrontendHttp = %d\ninternalFrontend = %d\n", o.Port, o.Port+10, o.Port+3)
	fmt.Fprintf(&b, "history = %d\nmatching = %d\nworker = %d\n", o.Port+1, o.Port+2, o.Port+6)
	b.WriteString("[database]\ntype = \"sqlite\"\n")
	if o.DBFile == "" {
		b.WriteString("inMemory = true\n")
	} else {
		b.WriteString("path = " + strconv.Quote(o.DBFile) + "\n")
	}
	return parse([]byte(b.String()), os.LookupEnv)
}
