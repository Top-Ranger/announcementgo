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

package plugin

import (
	"bytes"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/server"
	"github.com/Top-Ranger/announcementgo/translation"
	"github.com/Top-Ranger/auth/captcha"
	"github.com/domodwyer/mailyak"
)

func init() {
	var err error
	registerMailConfigTemplate, err = template.New("registerMailConfigTemplate").Parse(registerMailConfig)
	if err != nil {
		panic(err)
	}

	registerMailRegisterSiteTemplate, err = template.New("registerMailRegisterSiteTemplate").Parse(registerMailRegisterSite)
	if err != nil {
		panic(err)
	}

	registerMailUnregisterSiteTemplate, err = template.New("registerMailUnregisterSiteTemplate").Parse(registerMailUnregisterSite)
	if err != nil {
		panic(err)
	}

	err = registry.RegisterPlugin(registerMailFactory, "RegisterMail")
	if err != nil {
		panic(err)
	}
}

func registerMailFactory(key, shortDescription string, errorChannel chan string) (registry.Plugin, error) {
	r := new(registerMail)
	b, err := registry.CurrentDataSafe.GetConfig(key, "RegisterMail")
	if err != nil {
		return nil, err
	}
	if len(b) != 0 {
		buf := bytes.NewBuffer(b)
		dec := gob.NewDecoder(buf)
		err = dec.Decode(r)
		if err != nil {
			return nil, err
		}
	}
	r.l = new(sync.Mutex)
	r.key = key
	r.description = shortDescription
	r.e = errorChannel

	go r.sendWorker()

	server.AddHandle(key, "RegisterMail/subscribe.html", func(rw http.ResponseWriter, req *http.Request) {
		counter.StartProcess()
		r.l.Lock()
		defer r.l.Unlock()
		defer counter.EndProcess()

		if !r.verify() {
			rw.WriteHeader(http.StatusInternalServerError)
			t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}

		tl := translation.GetDefaultTranslation()

		switch req.Method {
		case http.MethodGet:
			id, c, err := captcha.GetStringsTimed(time.Now())
			if err != nil {
				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}
			td := registerMailRegisterSiteStruct{
				Description:      r.description,
				CaptchaID:        id,
				Captcha:          c,
				RegisterPassword: r.RegisterPassword != "",
				Translation:      tl,
			}
			var buf bytes.Buffer
			err = registerMailRegisterSiteTemplate.Execute(&buf, td)
			if err != nil {
				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}
			t := server.TextTemplateStruct{Text: template.HTML(buf.String()), Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return

		case http.MethodPost:
			err := req.ParseForm()
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}

			if req.Form.Get("dsgvo") == "" {
				rw.WriteHeader(http.StatusForbidden)
				t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}

			c := req.Form.Get("c")
			id := req.Form.Get("id")

			if !captcha.VerifyStringsTimed(id, c, time.Now(), 1*time.Hour) {
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailRegisterCaptchaFailure)), Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}

			if req.Form.Get("rp") != r.RegisterPassword {
				rw.WriteHeader(http.StatusForbidden)
				t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}

			m, err := mail.ParseAddress(req.Form.Get("mail"))
			if err != nil {
				rw.WriteHeader(http.StatusBadRequest)
				td := server.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
				server.TextTemplate.Execute(rw, td)
				return

			}

			for i := range r.ToData {
				if r.ToData[i].Hash {
					hash, err := base64.StdEncoding.DecodeString(r.ToData[i].Data)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						server.TextTemplate.Execute(rw, t)
						return
					}
					salt, err := base64.StdEncoding.DecodeString(r.ToData[i].Salt)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						server.TextTemplate.Execute(rw, t)
						return
					}
					if helper.VerifyHash([]byte(m.Address), hash, salt) {
						rw.WriteHeader(http.StatusBadRequest)
						td := server.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
						server.TextTemplate.Execute(rw, td)
						return
					}
				} else {
					if r.ToData[i].Data == m.Address {
						rw.WriteHeader(http.StatusBadRequest)
						td := server.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
						server.TextTemplate.Execute(rw, td)
						return
					}
				}
			}

			hash, salt, err := helper.Hash([]byte(m.Address))
			if err != nil {
				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}

			saltString := base64.StdEncoding.EncodeToString(salt)

			r.ToData = append(r.ToData, registerMailData{
				Data: base64.StdEncoding.EncodeToString(hash),
				Salt: saltString,
				Hash: true,
			})

			err = r.save()
			if err != nil {
				em := fmt.Sprintf("RegisterMail (%s): error while saving new register: %s", r.key, err.Error())
				log.Println(em)
				r.e <- em

				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}

			url := fmt.Sprintf("%s/RegisterMail/verify.html?key=%s&mail=%s", r.ServerName, url.QueryEscape(saltString), url.QueryEscape(m.Address))

			q := new(registerMailQueueObject)
			q.Announcement = registry.Announcement{
				Header:  r.description,
				Message: strings.Join([]string{r.RegisterMailText, url}, "\n\n"),
				Time:    time.Now(),
			}
			q.To = registerMailData{
				Data: m.Address,
				Salt: saltString,
				Hash: false,
			}
			r.Queue = append(r.Queue, q)

			t := server.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailRegisterSuccess)), Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return

		default:
			rw.WriteHeader(http.StatusMethodNotAllowed)
			t := server.TextTemplateStruct{Text: "405 Method Not Allowed", Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return
		}
	})

	server.AddHandle(key, "RegisterMail/unsubscribe.html", func(rw http.ResponseWriter, req *http.Request) {
		counter.StartProcess()
		r.l.Lock()
		defer r.l.Unlock()
		defer counter.EndProcess()

		tl := translation.GetDefaultTranslation()

		if !r.verify() {
			rw.WriteHeader(http.StatusInternalServerError)
			t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return
		}

		err := req.ParseForm()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return
		}

		switch req.Method {
		case http.MethodGet:
			td := registerMailUnregisterSiteStruct{
				Description: r.description,
				Salt:        req.Form.Get("key"),
				Mail:        req.Form.Get("mail"),
				Translation: tl,
			}
			var buf bytes.Buffer
			err := registerMailUnregisterSiteTemplate.Execute(&buf, td)
			if err != nil {
				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}
			t := server.TextTemplateStruct{Text: template.HTML(buf.String()), Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return

		case http.MethodPost:
			key := req.Form.Get("key")
			mail := req.Form.Get("mail")

			if key == "" || mail == "" {
				rw.WriteHeader(http.StatusForbidden)
				t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}

			for i := range r.ToData {
				if r.ToData[i].Salt == key {
					if !r.ToData[i].Hash && r.ToData[i].Data == mail {
						salt, err := base64.StdEncoding.DecodeString(r.ToData[i].Salt)
						if err != nil {
							log.Printf("RegisterMail (%s): %s", r.key, err.Error())
							rw.WriteHeader(http.StatusInternalServerError)
							t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
							server.TextTemplate.Execute(rw, t)
							return
						}

						hash := helper.HashForSalt([]byte(mail), salt)

						// Make data valid
						r.ToData[i].Data = base64.StdEncoding.EncodeToString(hash)
						r.ToData[i].Hash = true

						err = r.save()
						if err != nil {
							log.Printf("RegisterMail (%s): %s", r.key, err.Error())
							rw.WriteHeader(http.StatusInternalServerError)
							t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
							server.TextTemplate.Execute(rw, t)
							return
						}
					}
					t := server.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailUnregisterSuccessful)), Translation: tl}
					server.TextTemplate.Execute(rw, t)
					return
				}
			}

		default:
			rw.WriteHeader(http.StatusMethodNotAllowed)
			t := server.TextTemplateStruct{Text: "405 Method Not Allowed", Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return
		}
	})

	server.AddHandle(key, "RegisterMail/verify.html", func(rw http.ResponseWriter, req *http.Request) {
		counter.StartProcess()
		r.l.Lock()
		defer r.l.Unlock()
		defer counter.EndProcess()

		if !r.verify() {
			rw.WriteHeader(http.StatusInternalServerError)
			t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			server.TextTemplate.Execute(rw, t)
			return
		}

		tl := translation.GetDefaultTranslation()

		err := req.ParseForm()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return
		}

		key := req.Form.Get("key")
		mail := req.Form.Get("mail")

		if key == "" || mail == "" {
			rw.WriteHeader(http.StatusForbidden)
			t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
			server.TextTemplate.Execute(rw, t)
			return
		}

		for i := range r.ToData {
			if r.ToData[i].Salt == key {
				if r.ToData[i].Hash {
					hash, err := base64.StdEncoding.DecodeString(r.ToData[i].Data)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						server.TextTemplate.Execute(rw, t)
						return
					}
					salt, err := base64.StdEncoding.DecodeString(r.ToData[i].Salt)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						server.TextTemplate.Execute(rw, t)
						return
					}
					if !helper.VerifyHash([]byte(mail), hash, salt) {
						continue
					}

					// Make data valid
					r.ToData[i].Data = mail
					r.ToData[i].Hash = false

					err = r.save()
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := server.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						server.TextTemplate.Execute(rw, t)
						return
					}
				}
				t := server.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailValidationSuccess)), Translation: tl}
				server.TextTemplate.Execute(rw, t)
				return
			}
		}

		rw.WriteHeader(http.StatusForbidden)
		t := server.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
		server.TextTemplate.Execute(rw, t)
	})

	return r, nil
}

var registerMailConfigTemplate *template.Template

const registerMailConfig = `
<h1>RegisterMail</h1>
{{.ConfigValidFragment}}
<p id="RegisterMail_path"></p>
<form method="POST">
	<input type="hidden" name="target" value="RegisterMail">
	<p><input id="RegisterMail_prefix" type="text" name="prefix" value="{{.Prefix}}" placeholder="prefix"> <label for="RegisterMail_prefix">subject prefix</label></p>
	<p><input id="RegisterMail_from" type="text" name="from" value="{{.From}}" placeholder="from" required> <label for="RegisterMail_from">from</label></p>
	<p><input id="RegisterMail_server" type="text" name="server" value="{{.Server}}" placeholder="server" required> <label for="RegisterMail_server">SMTP server</label></p>
	<p><input id="RegisterMail_port" type="number" min="0" max="65535" step="1" name="port" value="{{.Port}}" placeholder="port" required> <label for="RegisterMail_port">SMTP port</label></p>
	<p><input id="RegisterMail_user" type="text" name="user" value="{{.User}}" placeholder="user" required> <label for="RegisterMail_user">user</label></p>
	<p><input id="RegisterMail_password" type="password" name="password" placeholder="password"> <label for="RegisterMail_password">password</label></p>
	<p><input id="RegisterMail_rate" type="number" min="0" step="1" name="rate" value="{{.RateLimit}}" placeholder="rate" required> <label for="RegisterMail_rate">rate limit (per minute)</label></p>
	<p><input id="RegisterMail_registermailtext" type="text" name="registermailtext" value="{{.RegisterMailText}}" placeholder="register mail text" required> <label for="RegisterMail_registermailtext">text for initial register confirmation mail</label></p>
	<p><input id="RegisterMail_unregisterlinktext" type="text" name="unregisterlinktext" value="{{.UnregisterLinkText}}" placeholder="unregister link text" required> <label for="RegisterMail_unregisterlinktext">text displayed before unregister link on every mail</label></p>
	<p><input id="RegisterMail_registerpassword" type="text" name="registerpassword" value="{{.RegisterPassword}}" placeholder="register password"> <label for="RegisterMail_registerpassword">password required for registering (leave empty for no password)</label></p>
	<p><input id="RegisterMail_thisserver" type="text" name="thisserver" value="" placeholder="server" required readonly> <label for="RegisterMail_thisserver">this server</label></p>
	<p><input type="submit" value="Update"></p>
	<details>
	<summary>Known users</summary>
	<ul>
	{{range $i, $e := .KnownUser}}
	<li>{{$e}}</li>
	{{end}}
	</ul>
	</details>
</form>

<script>
var link = document.createElement("A");
link.href = document.location.href +  "/RegisterMail/subscribe.html";
link.textContent = link.href;
var t = document.getElementById("RegisterMail_path");
t.appendChild(link)

document.getElementById("RegisterMail_thisserver").value = document.location.href;
</script>
`

type registerMailConfigTemplateStruct struct {
	Valid               bool
	ConfigValidFragment template.HTML
	Prefix              string
	From                string
	ToData              []registerMailData
	Server              string
	Port                int
	User                string
	RateLimit           int
	RegisterMailText    string
	UnregisterLinkText  string
	RegisterPassword    string
	KnownUser           []string
}

var registerMailRegisterSiteTemplate *template.Template

const registerMailRegisterSite = `
<h1>{{.Description}}</h1>
<p>{{.Translation.RegisterMailRegister}}</p>
<form method="POST">
   <input type="hidden" name="id" value="{{.CaptchaID}}">
   <p>E-Mail: <br> <input type="email" name="mail" placeholder="e-mail" required></p>
   <p><strong>Captcha</strong> {{.Captcha}}<br> <input type="captcha" name="c" placeholder="captcha" required></p>
   {{if .RegisterPassword}}
   <p>{{.Translation.Password}} <br> <input type="password" name="rp" placeholder="{{.Translation.Password}}" required></p>
   {{end}}
   <p><input type="checkbox" id="dsgvo" name="dsgvo" required><label for="dsgvo">{{.Translation.AcceptPrivacyPolicy}}</label></p>
   <p><input type="submit" value="{{.Translation.RegisterMailRegisterNow}}"></p>
</form>
`

type registerMailRegisterSiteStruct struct {
	Description      string
	CaptchaID        string
	Captcha          string
	RegisterPassword bool
	Translation      translation.Translation
}

var registerMailUnregisterSiteTemplate *template.Template

const registerMailUnregisterSite = `
<h1>{{.Description}}</h1>
<p>{{.Translation.RegisterMailUnregister}}</p>
<form method="POST">
   <input type="hidden" name="id" value="{{.Salt}}">
   <p>E-Mail: <br> <input type="email" name="mail" value={{.Mail}} placeholder="e-mail" required readonly></p>
   <p><input type="submit" value="{{.Translation.RegisterMailUnregister}}"></p>
</form>
`

type registerMailUnregisterSiteStruct struct {
	Description string
	Salt        string
	Mail        string
	Translation translation.Translation
}

//TODO: Delete

type registerMailData struct {
	Data string
	Salt string
	Hash bool
}

type registerMailQueueObject struct {
	To           registerMailData
	Announcement registry.Announcement
}

type registerMail struct {
	SubjectPrefix      string
	From               mail.Address
	ToData             []registerMailData
	SMTPServer         string
	SMTPServerPort     int
	SMTPUser           string
	SMTPPassword       string
	RateLimit          int
	RegisterMailText   string
	UnregisterLinkText string
	RegisterPassword   string
	ServerName         string
	Queue              []*registerMailQueueObject

	l           *sync.Mutex
	key         string
	e           chan string
	description string
}

func (r registerMail) verify() bool {
	// Caller has to lock l
	if r.From.Address == "" {
		return false
	}

	if r.SMTPServer == "" {
		return false
	}
	if r.SMTPUser == "" {
		return false
	}
	if r.SMTPPassword == "" {
		return false
	}
	if r.SMTPServerPort < 0 || r.SMTPServerPort > 65535 {
		return false
	}

	if r.RateLimit < 0 {
		return false
	}

	if r.RegisterMailText == "" {
		return false
	}

	if r.UnregisterLinkText == "" {
		return false
	}

	if r.ServerName == "" {
		return false
	}
	return true
}

func (r *registerMail) save() error {
	// Caller needs to lock
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(r)
	if err != nil {
		return err
	}
	err = registry.CurrentDataSafe.SetConfig(r.key, "RegisterMail", buf.Bytes())
	return err
}

func (r registerMail) GetConfig() template.HTML {
	counter.StartProcess()
	defer counter.EndProcess()
	r.l.Lock()
	defer r.l.Unlock()

	td := registerMailConfigTemplateStruct{
		Valid:              r.verify(),
		Prefix:             r.SubjectPrefix,
		From:               r.From.String(),
		Server:             r.SMTPServer,
		Port:               r.SMTPServerPort,
		User:               r.SMTPUser,
		RateLimit:          r.RateLimit,
		RegisterMailText:   r.RegisterMailText,
		UnregisterLinkText: r.UnregisterLinkText,
		RegisterPassword:   r.RegisterPassword,
	}

	for i := range r.ToData {
		if !r.ToData[i].Hash {
			td.KnownUser = append(td.KnownUser, r.ToData[i].Data)
		}
	}

	td.ConfigValidFragment = helper.ConfigInvalid
	if td.Valid {
		td.ConfigValidFragment = helper.ConfigValid
	}
	var buf bytes.Buffer
	err := registerMailConfigTemplate.Execute(&buf, td)
	if err != nil {
		log.Printf("RegisterMail (%s): %s", r.key, err.Error())
	}
	return template.HTML(buf.String())
}

func (r *registerMail) ProcessConfigChange(req *http.Request) error {
	counter.StartProcess()
	defer counter.EndProcess()
	err := req.ParseForm()
	if err != nil {
		return err
	}

	r.l.Lock()
	defer r.l.Unlock()

	r.SubjectPrefix = req.Form.Get("prefix") // Always set prefix to allow clearing it

	if req.Form.Get("from") != "" {
		a, err := mail.ParseAddress(req.Form.Get("from"))
		if err != nil {
			return err
		}
		r.From = *a
	}

	if req.Form.Get("server") != "" {
		r.SMTPServer = req.Form.Get("server")
	}

	if req.Form.Get("port") != "" {
		i, err := strconv.Atoi(req.Form.Get("port"))
		if err != nil {
			return err
		}
		if i < 0 || i > 65535 {
			return fmt.Errorf("Port %d out of range", i)
		}
		r.SMTPServerPort = i
	}

	if req.Form.Get("user") != "" {
		r.SMTPUser = req.Form.Get("user")
	}

	if req.Form.Get("password") != "" {
		r.SMTPPassword = req.Form.Get("password")
	}

	if req.Form.Get("rate") != "" {
		i, err := strconv.Atoi(req.Form.Get("rate"))
		if err != nil {
			return err
		}
		if i < 0 {
			return fmt.Errorf("Rate limit %d out of range", i)
		}
		r.RateLimit = i
	}

	r.RegisterMailText = req.Form.Get("registermailtext")
	r.UnregisterLinkText = req.Form.Get("unregisterlinktext")
	r.RegisterPassword = req.Form.Get("registerpassword")

	r.ServerName = strings.TrimSuffix(req.Form.Get("thisserver"), "/")

	return r.save()
}

func (r *registerMail) NewAnnouncement(a registry.Announcement, id string) {
	counter.StartProcess()
	defer counter.EndProcess()
	r.l.Lock()
	defer r.l.Unlock()

	for i := range r.ToData {
		if r.ToData[i].Hash {
			// This is no mail address - skip
			continue
		}

		url := fmt.Sprintf("%s/RegisterMail/unsubscribe.html?key=%s&mail=%s", r.ServerName, url.QueryEscape(r.ToData[i].Salt), url.QueryEscape(r.ToData[i].Data))

		q := new(registerMailQueueObject)
		q.Announcement = registry.Announcement{
			Header:  a.Header,
			Message: strings.Join([]string{a.Message, r.UnregisterLinkText, url}, "\n\n"),
			Time:    a.Time,
		}
		q.To = r.ToData[i]
		r.Queue = append(r.Queue, q)
	}

	err := r.save()
	if err != nil {
		em := fmt.Sprintf("RegisterMail (%s): error while saving queue : %s", r.key, err.Error())
		log.Println(em)
		r.e <- em
	}
}

func (r *registerMail) sendWorker() {
	for {
		// Wait for initial correct configuration
		r.l.Lock()
		if r.verify() {
			r.l.Unlock()
			break
		}
		r.l.Unlock()
		time.Sleep(1 * time.Minute)
	}

	for {
		time.Sleep(1 * time.Minute)
		counter.StartProcess()
		r.l.Lock()

		number := r.RateLimit
		if number == 0 || number > len(r.Queue) {
			number = len(r.Queue)
		}

		process := r.Queue[:number]
		temp := make([]*registerMailQueueObject, 0, len(r.Queue)-number)
		r.Queue = append(temp, r.Queue[number:]...)

		for i := range process {
			if !r.verify() {
				em := fmt.Sprintf("RegisterMail (%s): no valid configuration, can not send announcement (%s) to %s", r.key, process[i].Announcement.Header, process[i].To.Data)
				log.Println(em)
				r.e <- em
				continue
			}

			if process[i].To.Hash {
				// This is no mail address - skip
				continue
			}

			mail := mailyak.New(fmt.Sprint(r.SMTPServer, ":", strconv.Itoa(r.SMTPServerPort)), smtp.PlainAuth("", r.SMTPUser, r.SMTPPassword, r.SMTPServer))

			mail.From(r.From.Address)
			mail.FromName(r.From.Name)

			if r.SubjectPrefix == "" {
				mail.Subject(process[i].Announcement.Header)
			} else {
				mail.Subject(strings.Join([]string{r.SubjectPrefix, process[i].Announcement.Header}, " "))
			}

			mail.Plain().Set(process[i].Announcement.Message)
			mail.HTML().Set(string(helper.Format([]byte(process[i].Announcement.Message))))

			mail.To(process[i].To.Data)
			err := mail.Send()
			if err != nil {
				em := fmt.Sprintf("RegisterMail (%s): error while sending announcement (%s): %s", r.key, process[i].Announcement.Header, err.Error())
				log.Println(em)
				r.e <- em
			}
		}
		err := r.save()
		if err != nil {
			em := fmt.Sprintf("RegisterMail (%s): error while saving queue: %s", r.key, err.Error())
			log.Println(em)
			r.e <- em
		}

		r.l.Unlock()
		counter.EndProcess()

	}
}