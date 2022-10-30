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

package plugin

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/server"
	"github.com/Top-Ranger/announcementgo/templates"
	"github.com/Top-Ranger/announcementgo/translation"
	"github.com/Top-Ranger/auth/captcha"
	"github.com/domodwyer/mailyak/v3"
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

	registerMailDeleteSiteTemplate, err = template.New("registerMailDeleteSiteTemplate").Parse(registerMailDeleteSite)
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
		if r.SMTPPassword != "" && r.SMTPPasswordHidden {
			r.SMTPPassword, err = helper.UnhidePassword(r.SMTPPassword)
			if err != nil {
				return nil, err
			}
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
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		tl := translation.GetDefaultTranslation()

		if !r.RegistrationOpen {
			t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailRegistrationClosed)), Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		switch req.Method {
		case http.MethodGet:
			id, c, err := captcha.GetStringsTimed(time.Now())
			if err != nil {
				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
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
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}
			t := templates.TextTemplateStruct{Text: template.HTML(buf.String()), Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return

		case http.MethodPost:
			err := req.ParseForm()
			if err != nil {
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			if req.Form.Get("dsgvo") == "" {
				rw.WriteHeader(http.StatusForbidden)
				t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			c := req.Form.Get("c")
			id := req.Form.Get("id")

			if !captcha.VerifyStringsTimed(id, c, time.Now(), 1*time.Hour) {
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailRegisterCaptchaFailure)), Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			if req.Form.Get("rp") != r.RegisterPassword {
				rw.WriteHeader(http.StatusForbidden)
				t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			m, err := mail.ParseAddress(req.Form.Get("mail"))
			if err != nil {
				rw.WriteHeader(http.StatusBadRequest)
				td := templates.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
				templates.TextTemplate.Execute(rw, td)
				return

			}

			for i := range r.ToData {
				if r.ToData[i].Hash {
					hash, err := base64.StdEncoding.DecodeString(r.ToData[i].Data)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}
					salt, err := base64.StdEncoding.DecodeString(r.ToData[i].Salt)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}
					if helper.VerifyHash([]byte(m.Address), hash, salt) {
						rw.WriteHeader(http.StatusForbidden)
						td := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: translation.GetDefaultTranslation()}
						templates.TextTemplate.Execute(rw, td)
						return
					}
				} else {
					if r.ToData[i].Data == m.Address {
						rw.WriteHeader(http.StatusBadRequest)
						td := templates.TextTemplateStruct{Text: "400 Bad Request", Translation: translation.GetDefaultTranslation()}
						templates.TextTemplate.Execute(rw, td)
						return
					}
				}
			}

			hash, salt, err := helper.Hash([]byte(m.Address))
			if err != nil {
				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
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
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
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

			t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailRegisterSuccess)), Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return

		default:
			rw.WriteHeader(http.StatusMethodNotAllowed)
			t := templates.TextTemplateStruct{Text: "405 Method Not Allowed", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
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
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		err := req.ParseForm()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
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
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}
			t := templates.TextTemplateStruct{Text: template.HTML(buf.String()), Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return

		case http.MethodPost:
			key := req.Form.Get("key")
			mail := req.Form.Get("mail")

			if key == "" || mail == "" {
				rw.WriteHeader(http.StatusForbidden)
				t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			for i := range r.ToData {
				if r.ToData[i].Salt == key {
					if !r.ToData[i].Hash && r.ToData[i].Data == mail {
						// Data is not hashed

						// Completely remove data so that reregistration is possible
						r.ToData[i] = r.ToData[len(r.ToData)-1]
						r.ToData = r.ToData[:len(r.ToData)-1]

						err := r.save()
						if err != nil {
							log.Printf("RegisterMail (%s): %s", r.key, err.Error())
							rw.WriteHeader(http.StatusInternalServerError)
							t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
							templates.TextTemplate.Execute(rw, t)
							return
						}
						t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailUnregisterSuccessful)), Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}

					// Data is hashed
					hash, err := base64.StdEncoding.DecodeString(r.ToData[i].Data)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}
					salt, err := base64.StdEncoding.DecodeString(r.ToData[i].Salt)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}
					if helper.VerifyHash([]byte(mail), hash, salt) {
						t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailUnregisterSuccessful)), Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}

				}
			}

			rw.WriteHeader(http.StatusNotFound)
			t := templates.TextTemplateStruct{Text: "404 Not Found", Translation: tl}
			templates.TextTemplate.Execute(rw, t)

		default:
			rw.WriteHeader(http.StatusMethodNotAllowed)
			t := templates.TextTemplateStruct{Text: "405 Method Not Allowed", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}
	})

	adminRemove := func(rw http.ResponseWriter, req *http.Request, banforever bool) {
		counter.StartProcess()
		r.l.Lock()
		defer r.l.Unlock()
		defer counter.EndProcess()

		tl := translation.GetDefaultTranslation()

		if !r.verify() {
			rw.WriteHeader(http.StatusInternalServerError)
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		if _, admin := server.GetLogin(r.key, req); !admin {
			rw.WriteHeader(http.StatusForbidden)
			t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		err := req.ParseForm()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		switch req.Method {
		case http.MethodGet:
			td := registerMailDeleteSiteStruct{
				Description: r.description,
				Mail:        req.Form.Get("mail"),
				Ban:         banforever,
			}
			var buf bytes.Buffer
			err := registerMailDeleteSiteTemplate.Execute(&buf, td)
			if err != nil {
				log.Printf("RegisterMail (%s): %s", r.key, err.Error())
				rw.WriteHeader(http.StatusInternalServerError)
				t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}
			t := templates.TextTemplateStruct{Text: template.HTML(buf.String()), Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return

		case http.MethodPost:
			mail := req.Form.Get("mail")

			if mail == "" {
				rw.WriteHeader(http.StatusForbidden)
				t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}

			for i := range r.ToData {
				if !r.ToData[i].Hash && r.ToData[i].Data == mail {
					salt, err := base64.StdEncoding.DecodeString(r.ToData[i].Salt)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}

					if banforever {
						// Ban user
						hash := helper.HashForSalt([]byte(mail), salt)

						// Make data valid
						r.ToData[i].Data = base64.StdEncoding.EncodeToString(hash)
						r.ToData[i].Hash = true
					} else {
						// Delete user so that reregistration is possible
						r.ToData[i] = r.ToData[len(r.ToData)-1]
						r.ToData = r.ToData[:len(r.ToData)-1]

					}

					err = r.save()
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}
					t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(strings.Join([]string{mail, "deleted."}, " "))), Translation: tl}
					templates.TextTemplate.Execute(rw, t)
					return
				}
			}

			rw.WriteHeader(http.StatusNotFound)
			t := templates.TextTemplateStruct{Text: "404 Not Found", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		default:
			rw.WriteHeader(http.StatusMethodNotAllowed)
			t := templates.TextTemplateStruct{Text: "405 Method Not Allowed", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}
	}

	server.AddHandle(key, "RegisterMail/delete.html", func(rw http.ResponseWriter, req *http.Request) { adminRemove(rw, req, false) })
	server.AddHandle(key, "RegisterMail/ban.html", func(rw http.ResponseWriter, req *http.Request) { adminRemove(rw, req, true) })

	server.AddHandle(key, "RegisterMail/verify.html", func(rw http.ResponseWriter, req *http.Request) {
		counter.StartProcess()
		r.l.Lock()
		defer r.l.Unlock()
		defer counter.EndProcess()

		if !r.verify() {
			rw.WriteHeader(http.StatusInternalServerError)
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: translation.GetDefaultTranslation()}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		tl := translation.GetDefaultTranslation()

		if !r.RegistrationOpen {
			t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailRegistrationClosed)), Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		err := req.ParseForm()
		if err != nil {
			rw.WriteHeader(http.StatusInternalServerError)
			t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		key := req.Form.Get("key")
		mail := req.Form.Get("mail")

		if key == "" || mail == "" {
			rw.WriteHeader(http.StatusForbidden)
			t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
			templates.TextTemplate.Execute(rw, t)
			return
		}

		for i := range r.ToData {
			if r.ToData[i].Salt == key {
				if r.ToData[i].Hash {
					hash, err := base64.StdEncoding.DecodeString(r.ToData[i].Data)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}
					salt, err := base64.StdEncoding.DecodeString(r.ToData[i].Salt)
					if err != nil {
						log.Printf("RegisterMail (%s): %s", r.key, err.Error())
						rw.WriteHeader(http.StatusInternalServerError)
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
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
						t := templates.TextTemplateStruct{Text: "500 Internal Server Error", Translation: tl}
						templates.TextTemplate.Execute(rw, t)
						return
					}
				}
				t := templates.TextTemplateStruct{Text: template.HTML(template.HTMLEscapeString(tl.RegisterMailValidationSuccess)), Translation: tl}
				templates.TextTemplate.Execute(rw, t)
				return
			}
		}

		rw.WriteHeader(http.StatusForbidden)
		t := templates.TextTemplateStruct{Text: "403 Forbidden", Translation: tl}
		templates.TextTemplate.Execute(rw, t)
	})

	return r, nil
}

var registerMailConfigTemplate *template.Template

const registerMailRetries = 10

const registerMailConfig = `
<h1>RegisterMail</h1>
<p>Server must be connected over TLS</p>
{{.ConfigValidFragment}}
<p id="RegisterMail_path"></p>
<form method="POST">
	<input type="hidden" name="target" value="RegisterMail">
	<p><input id="RegisterMail_prefix" type="text" name="prefix" value="{{.Prefix}}" placeholder="prefix"> <label for="RegisterMail_prefix">subject prefix</label></p>
	<p><input id="RegisterMail_from" type="text" name="from" value="{{.From}}" placeholder="from" required> <label for="RegisterMail_from">from</label></p>
	<p><input id="RegisterMail_server" type="text" name="server" value="{{.Server}}" placeholder="server" required> <label for="RegisterMail_server">SMTP server</label></p>
	<p><input id="RegisterMail_port" type="number" min="0" max="65535" step="1" name="port" value="{{.Port}}" placeholder="port" required> <label for="RegisterMail_port">SMTP port</label></p>
	<p><input id="RegisterMail_user" type="text" name="user" value="{{.User}}" placeholder="user" required> <label for="RegisterMail_user">user</label></p>
	<p><input id="RegisterMail_password" type="password" name="password" placeholder="password"> <label for="RegisterMail_password">password (empty for disabling password protection)</label></p>
	<p><input id="RegisterMail_rate" type="number" min="0" step="1" name="rate" value="{{.RateLimit}}" placeholder="rate" required> <label for="RegisterMail_rate">rate limit (per minute)</label></p>
	<p><label for="RegisterMail_registermailtext">text for initial register confirmation mail</label></p> <textarea id="RegisterMail_registermailtext" name="registermailtext" rows="5" placeholder="register mail text" required>{{.RegisterMailText}}</textarea> <br>
	<p><label for="RegisterMail_unregisterlinktext">text displayed before unregister link on every mail</label></p> <textarea id="RegisterMail_unregisterlinktext" name="unregisterlinktext" rows="5" placeholder="unregister link text" required>{{.UnregisterLinkText}}</textarea> <br>
	<p><input id="RegisterMail_registerpassword" type="text" name="registerpassword" value="{{.RegisterPassword}}" placeholder="register password"> <label for="RegisterMail_registerpassword">password required for registering (leave empty for no password)</label></p>
	<p><input id="RegisterMail_open" type="checkbox" name="open" {{if .RegistrationOpen}}checked{{end}}> <label for="RegisterMail_open">registration open</label></p>
	<p><input id="RegisterMail_thisserver" type="text" name="thisserver" value="" placeholder="server" required readonly> <label for="RegisterMail_thisserver">this server</label></p>
	<p><input type="submit" value="Update"></p>
	<details>
	<summary>Known users</summary>
	<ul>
	{{range $i, $e := .KnownUser}}
	<li>{{$e}} <a href="{{$.ThisServer}}/RegisterMail/delete.html?mail={{$e}}" target="_blank">🗑</a> <a href="{{$.ThisServer}}/RegisterMail/ban.html?mail={{$e}}" target="_blank">🚫</a></li>
	{{end}}
	</ul>
	</details>
	<p></p>
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
	RegistrationOpen    bool
	RegisterPassword    string
	KnownUser           []string
	ThisServer          string
}

var registerMailRegisterSiteTemplate *template.Template

const registerMailRegisterSite = `
<h1>{{.Description}}</h1>
<p>{{.Translation.RegisterMailRegister}}</p>
<form method="POST">
   <input type="hidden" name="id" value="{{.CaptchaID}}">
   <p>{{.Translation.RegisterMailEMail}}: <br> <input type="email" name="mail" placeholder="{{.Translation.RegisterMailEMail}}" required></p>
   <p><strong>{{.Translation.RegisterMailCaptcha}}:</strong> <br> {{.Translation.CaptchaTextBefore}} {{.Captcha}} {{.Translation.CaptchaTextAfter}}<br> <input type="captcha" name="c" placeholder="{{.Translation.RegisterMailCaptcha}}" required autocomplete="off"></p>
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

var registerMailDeleteSiteTemplate *template.Template

const registerMailDeleteSite = `
<h1>{{.Description}}</h1>
<p>{{if .Ban}}Ban{{else}}Delete{{end}} user</p>
<form method="POST">
   <p>E-Mail: <br> <input type="email" name="mail" value={{.Mail}} placeholder="e-mail" required readonly></p>
   <p><input type="submit" {{if .Ban}}style="background-color: red;"{{end}} value="{{if .Ban}}Ban{{else}}Delete{{end}}"></p>
</form>
`

type registerMailDeleteSiteStruct struct {
	Description string
	Mail        string
	Ban         bool
}

type registerMailData struct {
	Data string
	Salt string
	Hash bool
}

type registerMailQueueObject struct {
	To             registerMailData
	Announcement   registry.Announcement
	NumberErrors   int
	UnsubscribeURL string
}

type registerMail struct {
	SubjectPrefix      string
	From               mail.Address
	ToData             []registerMailData
	SMTPServer         string
	SMTPServerPort     int
	SMTPUser           string
	SMTPPassword       string
	SMTPPasswordHidden bool
	RateLimit          int
	RegisterMailText   string
	UnregisterLinkText string
	RegisterPassword   string
	RegistrationOpen   bool
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
	tmpPw := r.SMTPPassword
	r.SMTPPassword = helper.HidePassword(r.SMTPPassword)
	r.SMTPPasswordHidden = true
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(r)
	r.SMTPPassword = tmpPw
	r.SMTPPasswordHidden = false
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
		RegistrationOpen:   r.RegistrationOpen,
		ThisServer:         r.ServerName,
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
			return fmt.Errorf("port %d out of range", i)
		}
		r.SMTPServerPort = i
	}

	if req.Form.Get("user") != "" {
		r.SMTPUser = req.Form.Get("user")
	}

	if req.Form.Get("password") != "" {
		r.SMTPPassword = req.Form.Get("password")
	}

	auth := smtp.PlainAuth("", r.SMTPUser, r.SMTPPassword, r.SMTPServer)
	conn, err := tls.Dial("tcp", strings.Join([]string{r.SMTPServer, strconv.Itoa(r.SMTPServerPort)}, ":"), &tls.Config{ServerName: r.SMTPServer, MinVersion: tls.VersionTLS12})
	if err != nil {
		r.SMTPPassword = ""
		return err
	}
	defer conn.Close()

	c, err := smtp.NewClient(conn, r.SMTPServer)
	if err != nil {
		r.SMTPPassword = ""
		return err
	}
	defer c.Close()

	err = c.Auth(auth)
	if err != nil {
		r.SMTPPassword = ""
		return err
	}

	if req.Form.Get("rate") != "" {
		i, err := strconv.Atoi(req.Form.Get("rate"))
		if err != nil {
			return err
		}
		if i < 0 {
			return fmt.Errorf("rate limit %d out of range", i)
		}
		r.RateLimit = i
	}

	r.RegisterMailText = req.Form.Get("registermailtext")
	r.UnregisterLinkText = req.Form.Get("unregisterlinktext")
	r.RegisterPassword = req.Form.Get("registerpassword")

	r.ServerName = strings.TrimSuffix(req.Form.Get("thisserver"), "/")

	r.RegistrationOpen = req.Form.Get("open") != ""

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
			Message: strings.Join([]string{a.Message, "\n***\n", r.UnregisterLinkText, url}, "\n\n"),
			Time:    a.Time,
		}
		q.To = r.ToData[i]
		q.UnsubscribeURL = url
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
		time.Sleep(1 * time.Minute)
		counter.StartProcess()
		r.l.Lock()

		if !r.verify() {
			// No valid configuration, jump out early
			r.l.Unlock()
			counter.EndProcess()
			continue
		}

		number := r.RateLimit
		if number == 0 || number > len(r.Queue) {
			number = len(r.Queue)
		}

		process := r.Queue[:number]
		temp := make([]*registerMailQueueObject, 0, len(r.Queue)-number)
		r.Queue = append(temp, r.Queue[number:]...)

		for i := range process {
			if process[i].To.Hash {
				// This is no mail address - skip
				continue
			}

			mail, err := mailyak.NewWithTLS(fmt.Sprint(r.SMTPServer, ":", strconv.Itoa(r.SMTPServerPort)), smtp.PlainAuth("", r.SMTPUser, r.SMTPPassword, r.SMTPServer), &tls.Config{ServerName: r.SMTPServer, MinVersion: tls.VersionTLS12})
			if err != nil {
				again := "final error"
				process[i].NumberErrors++
				if process[i].NumberErrors <= registerMailRetries {
					r.Queue = append(r.Queue, process[i])
					again = "trying again"
				}
				em := fmt.Sprintf("RegisterMail (%s): error while connecting to server (try: %d, %s): %s", r.key, process[i].NumberErrors, again, err.Error())
				log.Println(em)
				r.e <- em
				continue
			}

			mail.From(r.From.Address)
			mail.FromName(r.From.Name)

			if r.SubjectPrefix == "" {
				mail.Subject(process[i].Announcement.Header)
			} else {
				mail.Subject(strings.Join([]string{r.SubjectPrefix, process[i].Announcement.Header}, " "))
			}

			mail.Plain().Set(process[i].Announcement.Message)
			mail.HTML().Set(string(helper.Format([]byte(process[i].Announcement.Message))))

			if process[i].UnsubscribeURL != "" {
				mail.AddHeader("List-Unsubscribe", fmt.Sprintf("<%s>", process[i].UnsubscribeURL))
				mail.AddHeader("List-Unsubscribe-Post", "List-Unsubscribe=One-Click")
			}
			mail.AddHeader("List-Id", fmt.Sprintf("\"%s (AnnouncementGo! RegisterMail)\" <%s>", strings.ReplaceAll(r.description, "\"", ""), r.calculateListIdHost(r.ServerName)))

			mail.To(process[i].To.Data)
			err = mail.Send()
			if err != nil {
				again := "final error"
				process[i].NumberErrors++
				if process[i].NumberErrors <= registerMailRetries {
					r.Queue = append(r.Queue, process[i])
					again = "trying again"
				}
				em := fmt.Sprintf("RegisterMail (%s): error while sending announcement (%s; try: %d, %s): %s", r.key, process[i].Announcement.Header, process[i].NumberErrors, again, err.Error())
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

func (r registerMail) calculateListIdHost(URL string) string {
	URL = strings.TrimPrefix(URL, "http://")
	URL = strings.TrimPrefix(URL, "https://")
	split := strings.Split(URL, "/")
	if len(split) < 2 {
		return URL
	}
	removePort := strings.Split(split[0], ":")
	removePort = append(removePort, split[len(split)-1])
	sort.Slice(removePort, func(i, j int) bool { return j < i })
	return strings.Join(removePort, ".")
}
