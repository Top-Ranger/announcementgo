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

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
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
	"github.com/Top-Ranger/announcementgo/translation"
)

var knownKeys = make(map[string]bool)

type announcement struct {
	Key              string
	ShortDescription string
	Plugins          []string
	PasswordAdmin    []string
	PasswordUser     []string

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

		b, err := ioutil.ReadFile(path)
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

	plugins := make(map[string]bool, len(a.Plugins))

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

	for i := range a.PasswordAdmin {
		a.PasswordAdmin[i] = helper.EncodePassword(a.PasswordAdmin[i])
	}

	for i := range a.PasswordUser {
		a.PasswordUser[i] = helper.EncodePassword(a.PasswordUser[i])
	}

	err := server.AddHandle(a.Key, "", func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")

		// Test login
		loggedin, admin := server.GetLogin(a.Key, r)

		if !loggedin {
			td := server.LoginTemplateStruct{
				Key:              a.Key,
				ShortDescription: a.ShortDescription,
				Translation:      translation.GetDefaultTranslation(),
			}
			err := server.LoginTemplate.Execute(rw, td)
			if err != nil {
				log.Printf("login template: %s", err.Error())
			}
			return
		}

		// Process Request
		switch r.Method {
		case http.MethodGet:
			a.l.Lock()
			td := server.AnnouncementTemplateStruct{
				Key:              a.Key,
				Admin:            admin,
				ShortDescription: a.ShortDescription,
				Translation:      translation.GetDefaultTranslation(),
				Errors:           a.errors,
			}
			a.errors = nil
			a.l.Unlock()
			if admin {
				for i := range a.plugins {
					td.PluginConfig = append(td.PluginConfig, a.plugins[i].GetConfig())
				}
			}

			err := r.ParseForm()
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
				server.TextTemplate.Execute(rw, t)
			}
			td.Message = r.Form.Get("message")

			err = server.AnnouncementTemplate.Execute(rw, td)
			if err != nil {
				log.Println("announcement get:", err.Error())
			}
			return

		case http.MethodPost:
			err := r.ParseForm()
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
				server.TextTemplate.Execute(rw, t)
				return
			}

			switch r.Form.Get("target") {
			case "publish":
				if r.Form.Get("dsgvo") == "" {
					rw.WriteHeader(http.StatusPreconditionFailed)
					td := server.TextTemplateStruct{Text: "412 Precondition Failed", Translation: translation.GetDefaultTranslation()}
					server.TextTemplate.Execute(rw, td)
					return
				}

				subject := r.Form.Get("subject")
				message := r.Form.Get("message")
				if message == "" || subject == "" {
					rw.WriteHeader(http.StatusBadRequest)
					td := server.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
					server.TextTemplate.Execute(rw, td)
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
				td := server.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
				server.TextTemplate.Execute(rw, td)
				return
			}
		default:
			rw.WriteHeader(http.StatusBadRequest)
			t := server.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}
	})
	if err != nil {
		return err
	}

	err = server.AddHandle(a.Key, "login", func(rw http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			rw.WriteHeader(http.StatusBadRequest)
			t := server.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}
		err := r.ParseForm()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}

		password := r.Form.Get("password")
		if password == "" {
			rw.WriteHeader(http.StatusForbidden)
			t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}

		password = helper.EncodePassword(password)

		for i := range a.PasswordUser {
			if password == a.PasswordUser[i] {
				err := server.SetLoginCookie(a.Key, false, rw, r)
				if err != nil {
					rw.WriteHeader(http.StatusInternalServerError)
					t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
					server.TextTemplate.Execute(rw, t)
					return
				}
				http.Redirect(rw, r, fmt.Sprintf("/%s", a.Key), http.StatusSeeOther)
				return
			}
		}

		for i := range a.PasswordAdmin {
			if password == a.PasswordAdmin[i] {
				err := server.SetLoginCookie(a.Key, true, rw, r)
				if err != nil {
					rw.WriteHeader(http.StatusInternalServerError)
					t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
					server.TextTemplate.Execute(rw, t)
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
		t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: translation.GetDefaultTranslation()}
		server.TextTemplate.Execute(rw, t)
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
			t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}

		h, err := registry.CurrentDataSafe.GetAllAnnouncements(a.Key)
		if err != nil {
			log.Printf("announcement history (%s): %s", a.Key, err.Error())
			rw.WriteHeader(http.StatusInternalServerError)
			t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}

		td := server.HistoryTemplateStruct{
			Key:              a.Key,
			ShortDescription: a.ShortDescription,
			History:          h,
			Translation:      translation.GetDefaultTranslation(),
		}
		err = server.HistoryTemplate.Execute(rw, td)
		if err != nil {
			log.Printf("announcement history template (%s): %s", a.Key, err.Error())
		}
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
		a.errors = append(a.errors, e)
		a.l.Unlock()
		counter.EndProcess()
	}
}
