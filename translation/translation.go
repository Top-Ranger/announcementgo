// SPDX-License-Identifier: Apache-2.0
// Copyright 2020,2021,2022 Marcus Soll
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

package translation

import (
	"embed"
	"encoding/json"
	"log"
	"reflect"
	"strings"
	"sync"
)

//go:embed *.json
var translationJsons embed.FS

// Translation represents an object holding all translations
type Translation struct {
	Language                           string
	Subject                            string
	Message                            string
	PublishAnnouncement                string
	AnnouncementPublished              string
	PublishedAnnouncements             string
	Login                              string
	Logout                             string
	Password                           string
	CreatedBy                          string
	Impressum                          string
	PrivacyPolicy                      string
	AcceptPrivacyPolicy                string
	History                            string
	Back                               string
	CaptchaTextBefore                  string
	CaptchaTextAfter                   string
	DeleteMessages                     string
	BotAnswerMessage                   string
	BotSendOnThisChannel               string
	BotUserGreetings                   string
	RegisterMailRegister               string
	RegisterMailRegisterCaptchaFailure string
	RegisterMailRegisterNow            string
	RegisterMailRegisterSuccess        string
	RegisterMailValidationSuccess      string
	RegisterMailUnregister             string
	RegisterMailUnregisterSuccessful   string
	RegisterMailRegistrationClosed     string
	RegisterMailEMail                  string
	RegisterMailCaptcha                string
}

const defaultLanguage = "en"

var initialiseCurrent sync.Once
var current Translation
var rwlock sync.RWMutex

// GetTranslation returns a Translation struct of the given language.
// This function always loads translations from disk. Try to use GetDefaultTranslation where possible.
func GetTranslation(language string) (Translation, error) {
	t, err := getSingleTranslation(language)
	if err != nil {
		return Translation{}, err
	}
	d, err := getSingleTranslation(defaultLanguage)
	if err != nil {
		return Translation{}, err
	}

	// Set unknown strings to default value
	vp := reflect.ValueOf(&t)
	dv := reflect.ValueOf(d)
	v := vp.Elem()

	for i := 0; i < v.NumField(); i++ {
		if !v.Field(i).CanSet() {
			continue
		}
		if v.Field(i).Kind() != reflect.String {
			continue
		}
		if v.Field(i).String() == "" {
			v.Field(i).SetString(dv.Field(i).String())
		}
	}
	return t, nil
}

func getSingleTranslation(language string) (Translation, error) {
	if language == "" {
		return GetDefaultTranslation(), nil
	}

	file := strings.Join([]string{language, "json"}, ".")

	b, err := translationJsons.ReadFile(file)
	if err != nil {
		return Translation{}, err
	}
	t := Translation{}
	err = json.Unmarshal(b, &t)
	if err != nil {
		return Translation{}, err
	}
	return t, nil
}

// SetDefaultTranslation sets the default language to the provided one.
// Does nothing if it returns error != nil.
func SetDefaultTranslation(language string) error {
	if language == "" {
		return nil
	}

	t, err := GetTranslation(language)
	rwlock.Lock()
	defer rwlock.Unlock()
	if err != nil {
		return err
	}
	current = t
	return nil
}

// GetDefaultTranslation returns a Translation struct of the current default language.
func GetDefaultTranslation() Translation {
	initialiseCurrent.Do(func() {
		rwlock.RLock()
		c := current.Language
		rwlock.RUnlock()
		if c == "" {
			err := SetDefaultTranslation(defaultLanguage)
			if err != nil {
				log.Printf("Can not load default language (%s): %s", defaultLanguage, err.Error())
			}
		}
	})
	rwlock.RLock()
	defer rwlock.RUnlock()
	return current
}
