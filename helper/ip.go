// SPDX-License-Identifier: Apache-2.0
// Copyright 2020 Marcus Soll
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	  http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package helper

import (
	"net"
	"net/http"
	"strings"
)

// GetRealIP tries to fing the real IP address of a client.
// If an error is found, that error will be returned instead of an IP address.
// A reverse proxy is only assumed if address is a loopback device (to avoid spoofing)
func GetRealIP(r *http.Request) string {
	ipPart, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return err.Error()
	}
	ip := net.ParseIP(strings.SplitN(ipPart, "%", 1)[0])
	if ip != nil && !ip.IsLoopback() {
		// We found a valid IP, this is most likely correct
		goto returnIP
	}

	// This is likely behind a reverse proxy, try to find real address
	{
		header := r.Header.Get("Forwarded")
		if header != "" {
			headerParts := strings.Split(header, ";")
			for i := range headerParts {
				// Find for part of header
				if strings.HasPrefix(headerParts[i], "for=") {
					headerParts[i] = strings.TrimPrefix(headerParts[i], "for=")
					ip = processSplittedHeader(strings.Split(headerParts[i], ","))
					if ip != nil {
						goto returnIP
					}
				}
			}
		}
	}
	{
		header := r.Header.Get("X-Forwarded-For")
		if header != "" {
			ip = processSplittedHeader(strings.Split(header, ","))
			if ip != nil {
				goto returnIP
			}

		}
	}

returnIP:
	if ip == nil {
		return "unknown IP"
	}
	return ip.String()
}

func processSplittedHeader(split []string) net.IP {
	for i := len(split) - 1; i >= 0; i++ {
		// Go back to forward to find irst non local address. This way, fake addresses can't be spoofed by sending header (assumed proxy is trusted)

		// Assume entry is an IP address. Handle other cases later.
		s := split[i]
		// Case IPv6 with brackets - should do nothing in all other cases
		s = strings.TrimPrefix(s, "[")
		s = strings.TrimSuffix(s, "]")
		ip := net.ParseIP(strings.SplitN(s, "%", 1)[0])

		if ip == nil {
			// Maybe has form host:port?
			ipPart, _, err := net.SplitHostPort(split[i])
			if err != nil {
				// Invalid entry - something is wrong, stop processing
				return nil
			}
			ip := net.ParseIP(strings.SplitN(ipPart, "%", 1)[0])
			if ip == nil {
				// Invalid entry - something is wrong, stop processing
				return nil
			}
		}
		if ip != nil && !ip.IsLoopback() {
			return ip
		}
	}
	return nil
}
