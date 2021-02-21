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

package server

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/templates"
	"github.com/Top-Ranger/announcementgo/translation"
	"github.com/Top-Ranger/auth/data"
)

var serverMutex sync.Mutex
var serverStarted bool
var server http.Server
var initialised sync.Once

var dsgvo []byte
var impressum []byte

//go:embed css/* static/* font/*
var cachedFiles embed.FS
var etagCompare string
var cookieTime = 60

var robottxt = []byte(`User-agent: *
Disallow: /`)

// Config holds all server configuration.
type Config struct {
	Address          string
	PathDSGVO        string
	PathImpressum    string
	CookieTimeMinute int
}

// SetLoginCookie creates a valid login cookie for the given key.
func SetLoginCookie(key string, admin bool, rw http.ResponseWriter, r *http.Request) error {
	name := fmt.Sprintf("%s#user", key)
	if admin {
		name = fmt.Sprintf("%s#admin", key)
	}
	auth, err := data.GetStringsTimed(time.Now(), name)
	if err != nil {
		return err
	}
	cookie := http.Cookie{}
	cookie.Name = name
	cookie.Value = auth
	cookie.MaxAge = 60 * cookieTime
	cookie.SameSite = http.SameSiteLaxMode
	cookie.HttpOnly = true
	http.SetCookie(rw, &cookie)
	return nil
}

// RemoveLoginCookie removes all cookies for the given key.
// Please note that if the user can recreate the cookies on his machine, he can still log in.
func RemoveLoginCookie(key string, rw http.ResponseWriter, r *http.Request) {
	cookie := http.Cookie{}
	cookie.Name = fmt.Sprintf("%s#admin", key)
	cookie.Value = ""
	cookie.MaxAge = -1
	http.SetCookie(rw, &cookie)

	cookie = http.Cookie{}
	cookie.Name = fmt.Sprintf("%s#user", key)
	cookie.Value = ""
	cookie.MaxAge = -1
	http.SetCookie(rw, &cookie)
}

// GetLogin returns whether the user has a valid login and whether he is administrator.
func GetLogin(key string, r *http.Request) (loggedin, admin bool) {
	c := r.Cookies()
	adminCookie := fmt.Sprintf("%s#admin", key)
	userCookie := fmt.Sprintf("%s#user", key)
	for i := range c {
		if c[i].Name == userCookie {
			b := data.VerifyStringsTimed(c[i].Value, c[i].Name, time.Now(), time.Duration(cookieTime)*time.Minute)
			if b {
				loggedin = true
				return
			}
		}
		if c[i].Name == adminCookie {
			b := data.VerifyStringsTimed(c[i].Value, c[i].Name, time.Now(), time.Duration(cookieTime)*time.Minute)
			if b {
				loggedin = true
				admin = true
				return
			}
		}
	}
	return
}

// InitialiseServer initialises the server. It needs to be called first before calling any other funtion of this package.
func InitialiseServer(config Config) error {
	serverMutex.Lock()
	defer serverMutex.Unlock()
	if serverStarted {
		return nil
	}

	cookieTime = config.CookieTimeMinute

	// Guard to only initialise once
	serverInitialised := true
	initialised.Do(func() { serverInitialised = false })
	if serverInitialised {
		return fmt.Errorf("server: already initialised")
	}

	// Do setup
	server = http.Server{Addr: config.Address}

	// DSGVO
	b, err := os.ReadFile(config.PathDSGVO)
	if err != nil {
		return err
	}
	text := templates.TextTemplateStruct{Text: helper.Format(b), Translation: translation.GetDefaultTranslation()}
	output := bytes.NewBuffer(make([]byte, 0, len(text.Text)*2))
	templates.TextTemplate.Execute(output, text)
	dsgvo = output.Bytes()

	http.HandleFunc("/dsgvo.html", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(dsgvo)
	})

	// Impressum
	b, err = os.ReadFile(config.PathImpressum)
	if err != nil {
		return err
	}
	text = templates.TextTemplateStruct{Text: helper.Format(b), Translation: translation.GetDefaultTranslation()}
	output = bytes.NewBuffer(make([]byte, 0, len(text.Text)*2))
	templates.TextTemplate.Execute(output, text)
	impressum = output.Bytes()
	http.HandleFunc("/impressum.html", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(impressum)
	})

	etag := fmt.Sprint("\"", strconv.FormatInt(time.Now().Unix(), 10), "\"")
	etagCompare := strings.TrimSuffix(etag, "\"")
	etagCompareApache := strings.Join([]string{etagCompare, "-"}, "")       // Dirty hack for apache2, who appends -gzip inside the quotes if the file is compressed, thus preventing If-None-Match matching the ETag
	etagCompareCaddy := strings.Join([]string{"W/", etagCompare, "\""}, "") // Dirty hack for caddy, who appends W/ before the quotes if the file is compressed, thus preventing If-None-Match matching the ETag

	staticHandle := func(rw http.ResponseWriter, r *http.Request) {
		// Check for ETag
		v, ok := r.Header["If-None-Match"]
		if ok {
			for i := range v {
				if v[i] == etag || v[i] == etagCompareCaddy || strings.HasPrefix(v[i], etagCompareApache) {
					rw.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		// Send file if existing in cache
		path := r.URL.Path
		path = strings.TrimPrefix(path, "/")
		data, err := cachedFiles.Open(path)
		if err != nil {
			rw.WriteHeader(http.StatusNotFound)
		} else {
			rw.Header().Set("ETag", etag)
			rw.Header().Set("Cache-Control", "public, max-age=43200")
			switch {
			case strings.HasSuffix(path, ".svg"):
				rw.Header().Set("Content-Type", "image/svg+xml")
			case strings.HasSuffix(path, ".css"):
				rw.Header().Set("Content-Type", "text/css")
			case strings.HasSuffix(path, ".ttf"):
				rw.Header().Set("Content-Type", "application/x-font-truetype")
			case strings.HasSuffix(path, ".js"):
				rw.Header().Set("Content-Type", "application/javascript")
			default:
				rw.Header().Set("Content-Type", "text/plain")
			}
			io.Copy(rw, data)
		}
	}

	http.HandleFunc("/css/", staticHandle)
	http.HandleFunc("/static/", staticHandle)
	http.HandleFunc("/font/", staticHandle)
	http.HandleFunc("/js/", staticHandle)

	http.HandleFunc("/favicon.ico", func(rw http.ResponseWriter, r *http.Request) {
		// Check for ETag
		v, ok := r.Header["If-None-Match"]
		if ok {
			for i := range v {
				if v[i] == etag || v[i] == etagCompareCaddy || strings.HasPrefix(v[i], etagCompareApache) {
					rw.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		f, err := cachedFiles.Open("static/favicon.ico")

		if err != nil {
			rw.WriteHeader(http.StatusNotFound)
			return
		}
		io.Copy(rw, f)
	})

	// robots.txt
	http.HandleFunc("/robots.txt", func(rw http.ResponseWriter, r *http.Request) {
		rw.Write(robottxt)
	})

	http.HandleFunc("/", rootHandle)
	return nil
}

func rootHandle(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		tl := translation.GetDefaultTranslation()
		t := templates.TextTemplateStruct{Text: template.HTML("AnnouncementGo!"), Translation: tl}
		templates.TextTemplate.Execute(rw, t)
		return
	}
	tl := translation.GetDefaultTranslation()
	t := templates.TextTemplateStruct{Text: template.HTML("404 Not Found"), Translation: tl}
	rw.WriteHeader(http.StatusNotFound)
	templates.TextTemplate.Execute(rw, t)
	return
}

// RunServer starts the actual server.
// It does nothing if a server is already started.
// It will return directly after the server is started.
func RunServer() {
	serverMutex.Lock()
	defer serverMutex.Unlock()
	if serverStarted {
		return
	}

	counter.StartProcess()

	serverInitialised := true
	initialised.Do(func() { serverInitialised = false })
	if !serverInitialised {
		log.Panicln("server: not initialised")
	}

	log.Println("server: Server starting at", server.Addr)
	serverStarted = true
	go func() {
		err := server.ListenAndServe()
		if err != http.ErrServerClosed {
			log.Println("server:", err)
		}
	}()
}

// StopServer shuts the server down.
// It will do nothing if the server is not started.
// It will return after the shutdown is completed.
func StopServer() {
	serverMutex.Lock()
	defer serverMutex.Unlock()
	if !serverStarted {
		return
	}

	defer counter.EndProcess()

	err := server.Shutdown(context.Background())
	if err == nil {
		log.Println("server: stopped")
	} else {
		log.Println("server:", err)
	}
}

// AddHandle adds a hanler to the server.
// It can be called by plugins and similar.
func AddHandle(key, handle string, h http.HandlerFunc) error {
	serverMutex.Lock()
	defer serverMutex.Unlock()

	serverInitialised := true
	initialised.Do(func() { serverInitialised = false })
	if !serverInitialised {
		return fmt.Errorf("server: not initialised")
	}

	key = strings.TrimPrefix(key, "/")
	key = strings.TrimSuffix(key, "/")
	handle = strings.TrimPrefix(handle, "/")

	if handle == "" {
		http.HandleFunc(strings.Join([]string{"", key}, "/"), h)
		return nil
	}

	http.HandleFunc(strings.Join([]string{"", key, handle}, "/"), h)
	return nil
}
