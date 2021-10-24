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

package plugin

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/translation"
	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
	"gopkg.in/tucnak/telebot.v2"
)

// see https://core.telegram.org/bots#3-how-do-i-create-a-bot

func init() {
	var err error
	telegramConfigTemplate, err = template.New("telegramConfigTemplate").Parse(telegramConfig)
	if err != nil {
		panic(err)
	}

	err = registry.RegisterPlugin(telegramFactory, "Telegram")
	if err != nil {
		panic(err)
	}

	telegramPolicy = bluemonday.NewPolicy()
	telegramPolicy.AllowElements("b", "strong", "i", "em", "u", "ins", "s", "strike", "del", "a", "code", "pre")
	telegramPolicy.RequireParseableURLs(true)
	telegramPolicy.AllowURLSchemes("http", "https")
	telegramPolicy.AllowAttrs("href").OnElements("a")
	telegramPolicy.AllowAttrs("class").OnElements("code")
}

func telegramFactory(key, shortDescription string, errorChannel chan string) (registry.Plugin, error) {
	t := new(telegram)
	b, err := registry.CurrentDataSafe.GetConfig(key, "Telegram")
	if err != nil {
		return nil, err
	}
	if len(b) != 0 {
		buf := bytes.NewBuffer(b)
		dec := gob.NewDecoder(buf)
		err = dec.Decode(t)
		if err != nil {
			return nil, err
		}
		if t.Token != "" && t.TokenHidden {
			t.Token, err = helper.UnhidePassword(t.Token)
			if err != nil {
				return nil, err
			}
		}
	}
	t.l = new(sync.Mutex)
	t.key = key
	t.e = errorChannel

	t.l.Lock()
	defer t.l.Unlock()

	err = t.update()

	go t.sendWorker()

	return t, err
}

const telegramConfig = `
<h1>Telegram</h1>
{{.ConfigValidFragment}}
{{if .URL}}
<p>{{.URL}}</p>
{{end}}
<p>{{.UserNumber}} users</p>
<form method="POST">
	<input type="hidden" name="target" value="Telegram">
	<p><input id="Telegram_token" type="text" name="token" value="{{.Token}}" placeholder="token"> <label for="Telegram_token">Telegram Bot API token</label></p>
	<p><input type="submit" value="Update"></p>
</form>
`
const telegramLimit = 3000 // Max len: 4.096, some buffer

var telegramConfigTemplate *template.Template
var telegramPolicy *bluemonday.Policy // special policy for Telegram HTML - see https://core.telegram.org/bots/api#html-style

type telegramConfigTemplateStruct struct {
	Valid               bool
	ConfigValidFragment template.HTML
	Token               string
	UserNumber          int
	URL                 string
}

type telegramMessage struct {
	Target  int64
	Message string
	Silent  bool
}

type telegram struct {
	Token       string
	TokenHidden bool
	Targets     []int64
	Messages    []telegramMessage

	bot          *telebot.Bot
	currentToken string
	l            *sync.Mutex
	e            chan string
	key          string
}

func (t *telegram) update() error {
	// Caller has to lock
	counter.StartProcess()
	defer counter.EndProcess()

	if t.currentToken != t.Token {
		if t.bot != nil {
			t.bot.Stop()
			time.Sleep(10 * time.Second)
			t.bot = nil
		}
	}

	if t.bot == nil && t.Token != "" {
		var err error
		t.bot, err = telebot.NewBot(telebot.Settings{
			Token:  t.Token,
			Poller: &telebot.LongPoller{Timeout: 11 * time.Second},
		})
		if err != nil {
			t.bot = nil
			em := fmt.Sprintln("telegram:", err)
			log.Println(em)
			t.e <- em
			return err
		}

		addedFunction := func(m *telebot.Message) {
			counter.StartProcess()
			defer counter.EndProcess()
			t.l.Lock()
			defer t.l.Unlock()

			newID := m.Chat.ID
			found := false
			for i := range t.Targets {
				if t.Targets[i] == newID {
					found = true
					break
				}
			}
			if newID == int64(t.bot.Me.ID) {
				found = true
			}
			if !found {
				t.Targets = append(t.Targets, newID)
			}
			t.update()
		}

		t.bot.Handle(telebot.OnAddedToGroup, addedFunction)
		t.bot.Handle("/start", addedFunction)

		messageFunc := func(m *telebot.Message) {
			counter.StartProcess()
			defer counter.EndProcess()
			t.l.Lock()

			if !m.FromGroup() && !m.FromChannel() && !m.IsService() {
				_, err = t.bot.Send(m.Chat, translation.GetDefaultTranslation().BotAnswerMessage, telebot.NoPreview)
				if err != nil {
					em := fmt.Sprintln("telegram:", err)
					log.Println(em)
					t.e <- em
				}
			}
			t.l.Unlock()
			addedFunction(m)
		}

		t.bot.Handle(telebot.OnText, messageFunc)
		t.bot.Handle(telebot.OnPhoto, messageFunc)
		t.bot.Handle(telebot.OnAudio, messageFunc)
		t.bot.Handle(telebot.OnAnimation, messageFunc)
		t.bot.Handle(telebot.OnDocument, messageFunc)
		t.bot.Handle(telebot.OnSticker, messageFunc)
		t.bot.Handle(telebot.OnVideo, messageFunc)
		t.bot.Handle(telebot.OnVoice, messageFunc)
		t.bot.Handle(telebot.OnVideoNote, messageFunc)
		t.bot.Handle(telebot.OnContact, messageFunc)
		t.bot.Handle(telebot.OnLocation, messageFunc)
		t.bot.Handle(telebot.OnVenue, messageFunc)

		t.bot.Handle(telebot.OnMigration, func(from, to int64) {
			counter.StartProcess()
			defer counter.EndProcess()
			t.l.Lock()
			defer t.l.Unlock()

			for i := range t.Targets {
				if t.Targets[i] == from {
					t.Targets[i] = to
				}
			}
			t.update()
		})

		t.currentToken = t.Token
		go t.bot.Start()
	}

	tmpToken := t.Token
	t.Token = helper.HidePassword(t.Token)
	t.TokenHidden = true
	var config bytes.Buffer
	enc := gob.NewEncoder(&config)
	err := enc.Encode(t)
	t.Token = tmpToken
	t.TokenHidden = false
	if err != nil {
		em := fmt.Sprintln("telegram:", err)
		log.Println(em)
		t.e <- em
		return err
	}
	err = registry.CurrentDataSafe.SetConfig(t.key, "Telegram", config.Bytes())
	if err != nil {
		em := fmt.Sprintln("telegram:", err)
		log.Println(em)
		t.e <- em
		return err
	}

	return nil
}

func (t *telegram) GetConfig() template.HTML {
	counter.StartProcess()
	defer counter.EndProcess()
	t.l.Lock()
	defer t.l.Unlock()

	td := telegramConfigTemplateStruct{
		Valid:      t.bot != nil,
		Token:      t.Token,
		UserNumber: len(t.Targets),
		URL:        "Create bot: https://core.telegram.org/bots#3-how-do-i-create-a-bot",
	}
	td.ConfigValidFragment = helper.ConfigInvalid
	if td.Valid {
		td.ConfigValidFragment = helper.ConfigValid
	}
	if t.bot != nil {
		td.URL = fmt.Sprintf("https://t.me/%s", url.PathEscape(t.bot.Me.Username))
	}
	var buf bytes.Buffer
	err := telegramConfigTemplate.Execute(&buf, td)
	if err != nil {
		log.Printf("telegram (%s): %s", t.key, err.Error())
	}
	return template.HTML(buf.String())

}

func (t *telegram) ProcessConfigChange(r *http.Request) error {
	counter.StartProcess()
	defer counter.EndProcess()
	err := r.ParseForm()
	if err != nil {
		return err
	}

	t.l.Lock()
	defer t.l.Unlock()

	t.Token = r.Form.Get("token")

	err = t.update()
	if err != nil {
		em := fmt.Sprintln("telegram:", err)
		log.Println(em)
		t.e <- em
		return err
	}

	return nil
}

func (t *telegram) NewAnnouncement(a registry.Announcement, id string) {
	counter.StartProcess()
	defer counter.EndProcess()
	t.l.Lock()
	defer t.l.Unlock()

	if t.bot == nil {
		// no bot configurated - jump out
		return
	}

	// Include Header
	a.Message = strings.Join([]string{a.Header, a.Message}, "\n\n")

	messageParts := make([]string, 0)
	parts := 0

	for len(a.Message) > telegramLimit { // Telegram message limit
		i := strings.LastIndex(a.Message[:telegramLimit], "\n") // Try split at new line

		if i <= 500 { // Don't create really short messages or no index found
			i = strings.LastIndex(a.Message[:telegramLimit], " ") // Try split at space

			if i <= 500 { // Ok, there is really no good split point
				i = telegramLimit - 500
			}
		}

		var newPart string
		newPart, a.Message = a.Message[:i], a.Message[i:]
		parts++
		a.Message = strings.TrimSpace(a.Message)
		messageParts = append(messageParts, newPart)
	}
	messageParts = append(messageParts, a.Message)
	if parts != 0 {
		for i := range messageParts {
			messageParts[i] = fmt.Sprintf("[%d/%d]\n%s", i+1, parts+1, messageParts[i])
		}
	}

	for tar := range t.Targets {
		for mp := range messageParts {
			t.Messages = append(t.Messages, telegramMessage{Message: messageParts[mp], Target: t.Targets[tar], Silent: mp != 0})
		}
	}

	err := t.update()
	if err != nil {
		em := fmt.Sprintln("telegram:", err)
		log.Println(em)
		t.e <- em
	}
}

func (t *telegram) sendWorker() {
	for {
		time.Sleep(2 * time.Second)

		func() {
			counter.StartProcess()
			defer counter.EndProcess()
			t.l.Lock()
			defer t.l.Unlock()

			if t.bot == nil {
				return
			}

			if len(t.Messages) == 0 {
				return
			}

			var err error
			message := t.Messages[0]
			t.Messages = t.Messages[1:]

			// Check if target still exists
			ok := false

			for i := range t.Targets {
				if t.Targets[i] == message.Target {
					ok = true
					break
				}
			}

			if !ok {
				return
			}

			// Format message
			message.Message, err = t.formatMessage(message.Message)
			if err != nil {
				em := fmt.Sprintln("telegram:", err)
				log.Println(em)
				t.e <- em
				return
			}

			// Send message
			c, err := t.bot.ChatByID(strconv.FormatInt(message.Target, 10))
			if err != nil {
				em := fmt.Sprintln("telegram:", err)
				log.Println(em)
				t.e <- em
				t.removeTarget(message.Target)
				err = t.update()
				if err != nil {
					em := fmt.Sprintln("telegram:", err)
					log.Println(em)
					t.e <- em
				}
				return
			}

			_, err = t.bot.Send(c, message.Message, &telebot.SendOptions{DisableWebPagePreview: true, ParseMode: telebot.ModeHTML, DisableNotification: message.Silent})
			if err != nil {

				apierror, ok := err.(*telebot.APIError)
				showError := !ok
				if ok {
					if apierror.Code != 400 && apierror.Code != 500 {
						t.removeTarget(message.Target)
					}
					showError = (apierror.Code) != 403 && (apierror.Code != 401)
				}
				if showError {
					em := fmt.Sprintln("telegram:", err)
					log.Println(em)
					t.e <- em
				}
			}
			err = t.update()
			if err != nil {
				em := fmt.Sprintln("telegram:", err)
				log.Println(em)
				t.e <- em
			}
		}()
	}
}

func (t *telegram) removeTarget(target int64) {
	// Caller has to lock and save
	newIDs := make([]int64, 0, len(t.Targets))
	for i := range t.Targets {
		if t.Targets[i] != target {
			newIDs = append(newIDs, t.Targets[i])
		}
	}
	t.Targets = newIDs
}

func (t *telegram) formatMessage(message string) (string, error) {
	buf := bytes.Buffer{}
	md := goldmark.New(goldmark.WithExtensions(extension.GFM), goldmark.WithRendererOptions(html.WithHardWraps()))
	err := md.Convert([]byte(message), &buf)
	if err != nil {
		return "", fmt.Errorf("error rendering markdown: %w", err)
	}

	// Work around for correct line breaks in Telegram message
	b := buf.Bytes()
	b = bytes.ReplaceAll(b, []byte("<br>"), []byte(""))
	b = bytes.ReplaceAll(b, []byte("</p>"), []byte("</p>\n"))

	return string(telegramPolicy.SanitizeBytes(b)), nil
}
