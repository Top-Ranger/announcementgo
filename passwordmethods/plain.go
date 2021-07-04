// SPDX-License-Identifier: Apache-2.0
// Copyright 2021 Marcus Soll
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

package passwordmethods

import (
	"crypto/rand"
	"crypto/sha512"
	"crypto/subtle"
	"log"

	"github.com/Top-Ranger/announcementgo/registry"
)

var plainHash = make([]byte, sha512.Size)

func init() {
	_, err := rand.Read(plainHash)
	if err != nil {
		log.Panicf("Can not get random: %s", err.Error())
	}

	// Compatibility with old questionnaires - no method equals old plain
	err = registry.RegisterPasswordMethod(MethodPlain, "")
	if err != nil {
		log.Panicln(err)
	}

	err = registry.RegisterPasswordMethod(MethodPlain, "plain")
	if err != nil {
		log.Panicln(err)
	}
}

// MethodPlain compares a password to the plain text truth.
// It tries to hide details of the original password like length
func MethodPlain(password, truth string) (bool, error) {
	hasher := sha512.New()
	hasher.Write(plainHash)
	hasher.Write([]byte(password))
	hash1 := hasher.Sum(nil)
	hasher.Reset()
	hasher.Write(plainHash)
	hasher.Write([]byte(truth))
	hash2 := hasher.Sum(nil)
	return subtle.ConstantTimeCompare(hash1, hash2) == 1, nil
}
