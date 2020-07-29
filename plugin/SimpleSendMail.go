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
	"crypto/tls"
	"encoding/gob"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"sync"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/domodwyer/mailyak"
)

func init() {
	var err error
	simpleSendMailConfigTemplate, err = template.New("simpleSendMailConfigTemplate").Parse(simpleSendMailConfig)
	if err != nil {
		panic(err)
	}

	err = registry.RegisterPlugin(simpleSendMailFactory, "SimpleSendMail")
	if err != nil {
		panic(err)
	}
}

func simpleSendMailFactory(key, shortDescription string, errorChannel chan string) (registry.Plugin, error) {
	s := new(simpleSendMail)
	b, err := registry.CurrentDataSafe.GetConfig(key, "SimpleSendMail")
	if err != nil {
		return nil, err
	}
	if len(b) != 0 {
		buf := bytes.NewBuffer(b)
		dec := gob.NewDecoder(buf)
		err = dec.Decode(s)
		if err != nil {
			return nil, err
		}
		if s.SMTPPassword != "" && s.SMTPPasswordHidden {
			s.SMTPPassword, err = helper.UnhidePassword(s.SMTPPassword)
			if err != nil {
				return nil, err
			}
		}
	}
	s.l = new(sync.Mutex)
	s.key = key
	s.e = errorChannel

	return s, nil
}

const simpleSendMailConfig = `
<h1>SimpleSendMail</h1>
{{.ConfigValidFragment}}
<form method="POST">
	<input type="hidden" name="target" value="SimpleSendMail">
	<p><input id="SimpleSendMail_prefix" type="text" name="prefix" value="{{.Prefix}}" placeholder="prefix"> <label for="SimpleSendMail_prefix">subject prefix</label></p>
	<p><input id="SimpleSendMail_from" type="text" name="from" value="{{.From}}" placeholder="from" required> <label for="SimpleSendMail_from">from</label></p>
	<p><input id="SimpleSendMail_to" type="text" name="to" value="{{.To}}" placeholder="to" required> <label for="SimpleSendMail_to">to (seperate by comma)</label></p>
	<p><input id="SimpleSendMail_server" type="text" name="server" value="{{.Server}}" placeholder="server" required> <label for="SimpleSendMail_server">SMTP server</label></p>
	<p><input id="SimpleSendMail_port" type="number" min="0" max="65535" step="1" name="port" value="{{.Port}}" placeholder="port" required> <label for="SimpleSendMail_port">SMTP port</label></p>
	<p><input id="SimpleSendMail_user" type="text" name="user" value="{{.User}}" placeholder="user" required> <label for="SimpleSendMail_user">user</label></p>
	<p><input id="SimpleSendMail_password" type="password" name="password" placeholder="password"> <label for="SimpleSendMail_password">password</label></p>
	<p><input type="submit" value="Update"></p>
</form>
`

var simpleSendMailConfigTemplate *template.Template

type simpleSendMailConfigTemplateStruct struct {
	Valid               bool
	ConfigValidFragment template.HTML
	Prefix              string
	From                string
	To                  string
	Server              string
	Port                int
	User                string
}

type simpleSendMail struct {
	SubjectPrefix      string
	From               mail.Address
	To                 []*mail.Address
	SMTPServer         string
	SMTPServerPort     int
	SMTPUser           string
	SMTPPassword       string
	SMTPPasswordHidden bool

	l   *sync.Mutex
	key string
	e   chan string
}

func (s simpleSendMail) verify() bool {
	// Caller has to lock l
	if s.From.Address == "" {
		return false
	}
	if len(s.To) == 0 {
		return false
	}

	for i := range s.To {
		if s.To[i] == nil || s.To[i].Address == "" {
			return false
		}
	}
	if s.SMTPServer == "" {
		return false
	}
	if s.SMTPUser == "" {
		return false
	}
	if s.SMTPPassword == "" {
		return false
	}
	if s.SMTPServerPort < 0 || s.SMTPServerPort > 65535 {
		return false
	}
	return true
}

func (s simpleSendMail) GetConfig() template.HTML {
	counter.StartProcess()
	defer counter.EndProcess()
	s.l.Lock()
	defer s.l.Unlock()

	td := simpleSendMailConfigTemplateStruct{
		Valid:  s.verify(),
		Prefix: s.SubjectPrefix,
		From:   s.From.String(),
		To:     "",
		Server: s.SMTPServer,
		Port:   s.SMTPServerPort,
		User:   s.SMTPUser,
	}
	td.ConfigValidFragment = helper.ConfigInvalid
	if td.Valid {
		td.ConfigValidFragment = helper.ConfigValid
	}
	tos := make([]string, len(s.To))
	for i := range s.To {
		if s.To[i] != nil {
			tos[i] = s.To[i].String()
		}
	}
	td.To = strings.Join(tos, ", ")
	var buf bytes.Buffer
	err := simpleSendMailConfigTemplate.Execute(&buf, td)
	if err != nil {
		log.Printf("SimpleSendMail (%s): %s", s.key, err.Error())
	}
	return template.HTML(buf.String())
}

func (s *simpleSendMail) ProcessConfigChange(r *http.Request) error {
	counter.StartProcess()
	defer counter.EndProcess()
	err := r.ParseForm()
	if err != nil {
		return err
	}

	s.l.Lock()
	defer s.l.Unlock()

	s.SubjectPrefix = r.Form.Get("prefix") // Always set prefix to allow clearing it

	if r.Form.Get("from") != "" {
		a, err := mail.ParseAddress(r.Form.Get("from"))
		if err != nil {
			return err
		}
		s.From = *a
	}

	if r.Form.Get("to") != "" {
		a, err := mail.ParseAddressList(r.Form.Get("to"))
		if err != nil {
			return err
		}
		s.To = a
	}

	if r.Form.Get("server") != "" {
		s.SMTPServer = r.Form.Get("server")
	}

	if r.Form.Get("port") != "" {
		i, err := strconv.Atoi(r.Form.Get("port"))
		if err != nil {
			return err
		}
		if i < 0 || i > 65535 {
			return fmt.Errorf("Port %d out of range", i)
		}
		s.SMTPServerPort = i
	}

	if r.Form.Get("user") != "" {
		s.SMTPUser = r.Form.Get("user")
	}

	if r.Form.Get("password") != "" {
		s.SMTPPassword = r.Form.Get("password")
	}

	auth := smtp.PlainAuth("", s.SMTPUser, s.SMTPPassword, s.SMTPServer)
	c, err := smtp.Dial(strings.Join([]string{s.SMTPServer, strconv.Itoa(s.SMTPServerPort)}, ":"))
	if err != nil {
		s.SMTPPassword = ""
		return err
	}
	defer c.Close()

	err = c.StartTLS(&tls.Config{ServerName: s.SMTPServer})
	if err != nil {
		s.SMTPPassword = ""
		return err
	}

	err = c.Auth(auth)
	if err != nil {
		s.SMTPPassword = ""
		return err
	}

	tmpPw := s.SMTPPassword
	s.SMTPPassword = helper.HidePassword(s.SMTPPassword)
	s.SMTPPasswordHidden = true
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err = enc.Encode(s)
	s.SMTPPassword = tmpPw
	s.SMTPPasswordHidden = false
	if err != nil {
		return err
	}
	err = registry.CurrentDataSafe.SetConfig(s.key, "SimpleSendMail", buf.Bytes())
	return err
}

func (s *simpleSendMail) NewAnnouncement(a registry.Announcement, id string) {
	counter.StartProcess()
	defer counter.EndProcess()
	s.l.Lock()
	defer s.l.Unlock()

	if !s.verify() {
		em := fmt.Sprintf("SimpleSendMail (%s): no valid configuration, can not send announcement (%s)", s.key, a.Header)
		log.Println(em)
		s.e <- em
		return
	}

	mail := mailyak.New(fmt.Sprint(s.SMTPServer, ":", strconv.Itoa(s.SMTPServerPort)), smtp.PlainAuth("", s.SMTPUser, s.SMTPPassword, s.SMTPServer))

	mail.From(s.From.Address)
	mail.FromName(s.From.Name)

	tos := make([]string, len(s.To))
	for i := range s.To {
		if s.To[i] != nil {
			tos[i] = s.To[i].Address
		}
	}
	mail.To(tos...)

	if s.SubjectPrefix == "" {
		mail.Subject(a.Header)
	} else {
		mail.Subject(strings.Join([]string{s.SubjectPrefix, a.Header}, " "))
	}

	mail.Plain().Set(a.Message)
	mail.HTML().Set(string(helper.Format([]byte(a.Message))))
	err := mail.Send()
	if err != nil {
		em := fmt.Sprintf("SimpleSendMail (%s): error while sending announcement (%s): %s", s.key, a.Header, err.Error())
		log.Println(em)
		s.e <- em
	}
}
