// SPDX-License-Identifier: Apache-2.0
// Copyright 2020,2021 Marcus Soll
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
	"encoding/base32"
)

// HidePassword encodes the password in a way it is no longer human readable.
// This function is easily reverseable. Its sole purpose is to not save passwords as clear text on disks (to avoid an accidental read).
// It provides no security against an attacker or bad actor.
func HidePassword(pw string) string {
	return base32.StdEncoding.EncodeToString([]byte(pw))
}

// UnhidePassword reveals a hidden password. See HidePassword for more information.
func UnhidePassword(pw string) (string, error) {
	b, err := base32.StdEncoding.DecodeString(pw)
	return string(b), err
}
