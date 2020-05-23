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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base32"
)

var passwordSalt = make([]byte, sha512.Size)

func init() {
	_, err := rand.Read(passwordSalt)
	if err != nil {
		panic(err)
	}
}

func EncodePassword(pw string) string {
	h := hmac.New(sha512.New, passwordSalt)
	h.Write([]byte(pw))
	return base32.StdEncoding.EncodeToString(h.Sum(nil))
}
