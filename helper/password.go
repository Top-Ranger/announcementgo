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
	"crypto/rand"
	"encoding/base32"

	"golang.org/x/crypto/argon2"
)

const passowrdEncodedLength = 33

var passwordSalt = make([]byte, passowrdEncodedLength)

func init() {
	_, err := rand.Read(passwordSalt)
	if err != nil {
		panic(err)
	}
}

func EncodePassword(pw string) string {
	key := argon2.IDKey([]byte(pw), passwordSalt, 1, 64*1024, 2, passowrdEncodedLength)
	return base32.StdEncoding.EncodeToString(key)
}
