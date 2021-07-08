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

package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/server"
	"github.com/Top-Ranger/announcementgo/templates"
	"github.com/Top-Ranger/announcementgo/translation"
)

var knownKeys = make(map[string]bool)

type announcement struct {
	Key                  string
	ShortDescription     string
	Plugins              []string
	UsersSeeErrors       bool
	UsersCanDeleteErrors bool
	PasswordMethod       string
	PasswordAdmin        []string
	PasswordUser         []string

	plugins []registry.Plugin
	errors  []string
	l       *sync.Mutex
}

// LoadAnnouncements loads all announcements in a path
func LoadAnnouncements(path string) error {
	err := filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(path) != ".json" {
			return nil
		}

		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		a := new(announcement)
		err = json.Unmarshal(b, a)
		if err != nil {
			return err
		}
		err = a.Initialise()
		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return fmt.Errorf("announcement: %w", err)
	}
	return nil
}

func (a *announcement) Initialise() error {
	a.l = new(sync.Mutex)
	a.l.Lock()
	defer a.l.Unlock()

	errorChannel := make(chan string, 100)

	if a.Key == "" {
		return fmt.Errorf("invalid key")
	}
	if strings.ContainsAny(a.Key, "#") {
		return fmt.Errorf("invalid character in key key")
	}
	a.Key = url.PathEscape(a.Key)

	ok := knownKeys[a.Key]
	if ok {
		return fmt.Errorf("key already in use")
	}
	knownKeys[a.Key] = true

	a.loadErrors()

	plugins := make(map[string]bool, len(a.Plugins))

	ok = registry.PasswordMethodExists(a.PasswordMethod)
	if !ok {
		return fmt.Errorf("password method '%s' not known", a.PasswordMethod)
	}

	for i := range a.Plugins {
		if plugins[a.Plugins[i]] {
			return fmt.Errorf("announcement: plugin %s found twice", a.Plugins[i])
		}
		pf, ok := registry.GetPlugin(a.Plugins[i])
		if !ok {
			return fmt.Errorf("announcement: unknown plugin %s", a.Plugins[i])
		}
		p, err := pf(a.Key, a.ShortDescription, errorChannel)
		if err != nil {
			return fmt.Errorf("announcement: plugin %s has error %w", a.Plugins[i], err)
		}
		a.plugins = append(a.plugins, p)
	}

	err := server.AddHandle(a.Key, "", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

		// Test login
		loggedin, admin := server.GetLogin(a.Key, r)

		if !loggedin {
			td := templates.LoginTemplateStruct{
				Key:              a.Key,
				ShortDescription: a.ShortDescription,
				Translation:      translation.GetDefaultTranslation(),
			}
			err := templates.LoginTemplate.Execute(rw, td)
			if err != nil {
				log.Printf("login template: %s", err.Error())
			}
			return
		}

		// Process Request
		switch r.Method {
		case http.MethodGet:
			a.l.Lock()
			td := templates.AnnouncementTemplateStruct{
				Key:              a.Key,
				Admin:            admin,
				ShortDescription: a.ShortDescription,
				Translation:      translation.GetDefaultTranslation(),
				Errors:           nil,
			}
			if admin || a.UsersSeeErrors {
				td.Errors = a.errors
			}
			if admin || a.UsersCanDeleteErrors {
				td.EnableDeleteErrors = true
			}
			a.l.Unlock()
			if admin {
				for i := range a.plugins {
					td.PluginConfig = append(td.PluginConfig, a.plugins[i].GetConfig())
				}
			}

			err := r.ParseForm()
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
				templates.TextTemplate.Execute(rw, t)
			}
			td.Message = r.Form.Get("message")

			err = templates.AnnouncementTemplate.Execute(rw, td)
			if err != nil {
				log.Println("announcement get:", err.Error())
			}
			return

		case http.MethodPost:
			err := r.ParseForm()
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			switch r.Form.Get("target") {
			case "publish":
				if r.Form.Get("dsgvo") == "" {
					rw.WriteHeader(http.StatusPreconditionFailed)
					td := templates.TextTemplateStruct{Text: "412 Precondition Failed", Translation: translation.GetDefaultTranslation()}
					templates.TextTemplate.Execute(rw, td)
					return
				}

				subject := r.Form.Get("subject")
				message := r.Form.Get("message")
				if message == "" || subject == "" {
					rw.WriteHeader(http.StatusBadRequest)
					td := templates.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
					templates.TextTemplate.Execute(rw, td)
					return
				}
				an := registry.Announcement{
					Header:  subject,
					Message: message,
					Time:    time.Now(),
				}
				id, err := registry.CurrentDataSafe.SaveAnnouncement(a.Key, an)
				if err != nil {
					log.Println("announcement save:", err.Error())
				}
				for i := range a.plugins {
					go a.plugins[i].NewAnnouncement(an, id)
				}
				http.Redirect(rw, r, fmt.Sprintf("/%s?message=%s", a.Key, url.QueryEscape(translation.GetDefaultTranslation().AnnouncementPublished)), http.StatusSeeOther)
				return
			default:
				t := r.Form.Get("target")
				for i := range a.Plugins {
					if t == a.Plugins[i] {
						err = a.plugins[i].ProcessConfigChange(r)
						if err != nil {
							log.Printf("announcement plugin config (%s): %s", a.Plugins[i], err.Error())
							http.Redirect(rw, r, fmt.Sprintf("/%s?message=%s", a.Key, url.QueryEscape(err.Error())), http.StatusSeeOther)
							return
						}
						http.Redirect(rw, r, fmt.Sprintf("/%s", a.Key), http.StatusSeeOther)
						return
					}
				}
				rw.WriteHeader(http.StatusBadRequest)
				td := templates.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
				templates.TextTemplate.Execute(rw, td)
				return
			}
		default:
			rw.WriteHeader(http.StatusBadRequest)
			t := templates.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}
	})
	if err != nil {
		return err
	}

	err = server.AddHandle(a.Key, "login", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			rw.WriteHeader(http.StatusBadRequest)
			t := templates.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}
		err := r.ParseForm()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		password := r.Form.Get("password")
		if password == "" {
			rw.WriteHeader(http.StatusForbidden)
			t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		for i := range a.PasswordUser {
			ok, err := registry.ComparePasswords(a.PasswordMethod, password, a.PasswordUser[i])
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			if ok {
				err := server.SetLoginCookie(a.Key, false, rw, r)
				if err != nil {
					rw.WriteHeader(http.StatusInternalServerError)
					t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
					templates.TextTemplate.Execute(rw, t)
					return
				}
				http.Redirect(rw, r, fmt.Sprintf("/%s", a.Key), http.StatusSeeOther)
				return
			}
		}

		for i := range a.PasswordAdmin {
			ok, err := registry.ComparePasswords(a.PasswordMethod, password, a.PasswordAdmin[i])
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			if ok {
				err := server.SetLoginCookie(a.Key, true, rw, r)
				if err != nil {
					rw.WriteHeader(http.StatusInternalServerError)
					t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
					templates.TextTemplate.Execute(rw, t)
					return
				}
				http.Redirect(rw, r, fmt.Sprintf("/%s", a.Key), http.StatusSeeOther)
				return
			}
		}

		if config.LogFailedLogin {
			log.Printf("Failed login from %s", helper.GetRealIP(r))
		}
		rw.WriteHeader(http.StatusForbidden)
		t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: translation.GetDefaultTranslation()}
		templates.TextTemplate.Execute(rw, t)
	})
	if err != nil {
		return err
	}

	err = server.AddHandle(a.Key, "logout", func(rw http.ResponseWriter, r *http.Request) {
		server.RemoveLoginCookie(a.Key, rw, r)
		http.Redirect(rw, r, fmt.Sprintf("/%s", a.Key), http.StatusSeeOther)
	})
	if err != nil {
		return err
	}

	err = server.AddHandle(a.Key, "history.html", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		loggedin, _ := server.GetLogin(a.Key, r)
		if !loggedin {
			rw.WriteHeader(http.StatusForbidden)
			t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		h, err := registry.CurrentDataSafe.GetAllAnnouncements(a.Key)
		if err != nil {
			log.Printf("announcement history (%s): %s", a.Key, err.Error())
			rw.WriteHeader(http.StatusInternalServerError)
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		td := templates.HistoryTemplateStruct{
			Key:              a.Key,
			ShortDescription: a.ShortDescription,
			History:          h,
			Translation:      translation.GetDefaultTranslation(),
		}
		err = templates.HistoryTemplate.Execute(rw, td)
		if err != nil {
			log.Printf("announcement history template (%s): %s", a.Key, err.Error())
		}
	})

	err = server.AddHandle(a.Key, "deleteErrors", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		loggedin, admin := server.GetLogin(a.Key, r)
		if !loggedin {
			rw.WriteHeader(http.StatusForbidden)
			return
		}

		if !(admin || a.UsersCanDeleteErrors) {
			rw.WriteHeader(http.StatusForbidden)
			return
		}

		counter.StartProcess()
		a.l.Lock()
		a.errors = nil
		a.saveErrors()
		a.l.Unlock()
		counter.EndProcess()
		rw.WriteHeader(http.StatusOK)
		return
	})

	go announcemetWorker(a, errorChannel)

	log.Println("announcement: sucessfully loaded", a.Key)
	return nil
}

func announcemetWorker(a *announcement, errorChannel chan string) {
	for {
		e := <-errorChannel
		counter.StartProcess()
		a.l.Lock()
		a.errors = append(a.errors, fmt.Sprintf("%s: %s", time.Now().Format(time.RFC3339), e))
		a.saveErrors()
		a.l.Unlock()
		counter.EndProcess()
	}
}

func (a *announcement) loadErrors() {
	// Caller needs to lock
	counter.StartProcess()
	defer counter.EndProcess()
	b, err := registry.CurrentDataSafe.GetConfig(a.Key, "internal##errors")
	if err != nil {
		log.Printf("loading errors (%s): %s", a.Key, err.Error())
		return
	}
	if len(b) == 0 {
		a.errors = nil
		return
	}
	buf := bytes.NewBuffer(b)
	dec := gob.NewDecoder(buf)
	err = dec.Decode(&a.errors)
	if err != nil {
		log.Printf("decoding errors (%s): %s", a.Key, err.Error())
		return
	}
}

func (a *announcement) saveErrors() {
	// Caller needs to lock
	if a.errors == nil {
		err := registry.CurrentDataSafe.SetConfig(a.Key, "internal##errors", nil)
		if err != nil {
			log.Printf("saving nil errors (%s): %s", a.Key, err.Error())
			return
		}
	}

	var config bytes.Buffer
	enc := gob.NewEncoder(&config)
	err := enc.Encode(&a.errors)
	if err != nil {
		log.Printf("encoding errors (%s): %s", a.Key, err.Error())
		return
	}
	err = registry.CurrentDataSafe.SetConfig(a.Key, "internal##errors", config.Bytes())
	if err != nil {
		log.Printf("saving errors (%s): %s", a.Key, err.Error())
		return
	}
}
