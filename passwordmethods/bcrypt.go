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
	"encoding/base64"
	"log"

	"github.com/Top-Ranger/announcementgo/registry"
	"golang.org/x/crypto/bcrypt"
)

func init() {
	err := registry.RegisterPasswordMethod(MethodBcryptPlain, "bcrypt_plain")
	if err != nil {
		log.Panicln(err)
	}

	err = registry.RegisterPasswordMethod(MethodBcrypt64, "bcrypt64")
	if err != nil {
		log.Panicln(err)
	}
}

// MethodBcryptPlain compares a password to a bcrypt hash in plain format.
func MethodBcryptPlain(password, truth string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(truth), []byte(password))
	return err == nil, nil
}

// MethodBcryptPlain compares a password to a base64-encoded bcrypt hash.
func MethodBcrypt64(password, truth string) (bool, error) {
	b, err := base64.StdEncoding.DecodeString(truth)
	if err != nil {
		return false, err
	}
	err = bcrypt.CompareHashAndPassword(b, []byte(password))
	return err == nil, nil
}
