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
	"crypto/subtle"
)

// Hash creates a hash of the data.
// The function will create a salt for the hash.
func Hash(data []byte) (hash, salt []byte, err error) {
	salt = make([]byte, sha512.Size)
	_, err = rand.Read(salt)
	h := hmac.New(sha512.New, salt)
	h.Write(data)
	hash = h.Sum([]byte("sha512:"))
	return
}

// HashForSalt creates a hash of the data.
// You have to provide a salt for the hash.
func HashForSalt(data, salt []byte) (hash []byte) {
	h := hmac.New(sha512.New, salt)
	h.Write(data)
	hash = h.Sum([]byte("sha512:"))
	return
}

// VerifyHash returns whether the data and the hash correspond (given the salt).
func VerifyHash(data, hash, salt []byte) bool {
	h := hmac.New(sha512.New, salt)
	h.Write(data)
	v := h.Sum([]byte("sha512:"))
	return subtle.ConstantTimeCompare(hash, v) == 1
}
