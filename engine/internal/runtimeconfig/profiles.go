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
	"slices"
	"strings"

	"github.com/knadh/koanf/v2"
	"go.temporal.io/server/common/primitives"
)

const (
	profileAll    = "all"
	profileEngine = "engine"
	// profileAPI is the API server's profile; the engine starts nothing for it.
	profileAPI = "api"
)

var engineServices = []string{
	string(primitives.FrontendService), string(primitives.InternalFrontendService), string(primitives.HistoryService),
	string(primitives.MatchingService), string(primitives.WorkerService),
}

// profileServices maps the [runtime] profiles to the Temporal services a node runs. The internal frontend runs with
// the frontend: Temporal's own services find it through membership.
var profileServices = map[string][]string{
	profileAll:    engineServices,
	profileEngine: engineServices,
	"frontend":    {string(primitives.FrontendService), string(primitives.InternalFrontendService)},
	"history":     {string(primitives.HistoryService)},
	"matching":    {string(primitives.MatchingService)},
	"worker":      {string(primitives.WorkerService)},
	profileAPI:    nil,
}

// services reads [runtime] profiles (["all"] when absent) and returns the services this node runs, in a stable order.
func services(k *koanf.Koanf) ([]string, error) {
	profiles := []string{profileAll}
	if k.Exists("runtime.profiles") {
		profiles = k.Strings("runtime.profiles")
		if len(profiles) == 0 {
			return nil, fmt.Errorf("invalid runtime configuration: runtime.profiles must name at least one profile")
		}
	}
	chosen := map[string]bool{}
	for _, p := range profiles {
		names, ok := profileServices[p]
		if !ok {
			known := make([]string, 0, len(profileServices))
			for name := range profileServices {
				known = append(known, name)
			}
			slices.Sort(known)
			return nil, fmt.Errorf("invalid runtime configuration: runtime.profiles: unknown profile %q (one of %s)",
				p, strings.Join(known, ", "))
		}
		for _, s := range names {
			chosen[s] = true
		}
	}
	var out []string
	for _, s := range engineServices {
		if chosen[s] {
			out = append(out, s)
		}
	}
	return out, nil
}
