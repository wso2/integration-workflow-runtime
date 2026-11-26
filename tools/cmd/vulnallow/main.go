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

// Command vulnallow reads govulncheck's JSON output on standard input and fails on any vulnerability the code calls,
// except those listed in an exceptions file: vulnallow <exceptions file>.
package main

import (
	"fmt"
	"os"

	"github.com/wso2/integration-workflow-runtime/tools/internal/vulnallow"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: govulncheck -format json ./... | vulnallow <exceptions file>")
		os.Exit(2)
	}
	allowed, err := vulnallow.ReadExceptions(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(vulnallow.Check(os.Stdin, allowed, os.Stdout, os.Stderr))
}
