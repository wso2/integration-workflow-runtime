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
	"errors"
	"net"
)

// autoAddress as engine.broadcastAddress picks this host's address, for containers whose IP is known only at start.
const autoAddress = "auto"

// interfaceAddrs is replaced in tests.
var interfaceAddrs = net.InterfaceAddrs

// hostAddress returns the first non-loopback IPv4 address, or else the first global IPv6 address.
func hostAddress() (string, error) {
	addrs, err := interfaceAddrs()
	if err != nil {
		return "", err
	}
	var v6 string
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() {
			continue
		}
		if v4 := n.IP.To4(); v4 != nil {
			return v4.String(), nil
		}
		if v6 == "" && n.IP.IsGlobalUnicast() {
			v6 = n.IP.String()
		}
	}
	if v6 != "" {
		return v6, nil
	}
	return "", errors.New("this host has no non-loopback address")
}
