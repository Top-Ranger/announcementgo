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
	"encoding/gob"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/registry"
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
	}
	t.l = new(sync.Mutex)
	t.key = key
	t.e = errorChannel

	t.l.Lock()
	defer t.l.Unlock()

	err = t.update()

	return t, err
}

const telegramConfig = `
<h1>Telegram</h1>
{{.ConfigValidFragment}}
<p>{{.UserNumber}} users</p>
<form method="POST">
	<input type="hidden" name="target" value="Telegram">
	<p><input id="Telegram_token" type="text" name="token" value="{{.Token}}" placeholder="token"> <label for="Telegram_token">Telegram Bot API token</label></p>
	<p><input type="submit" value="Update"></p>
</form>
`

var telegramConfigTemplate *template.Template

type telegramConfigTemplateStruct struct {
	Valid               bool
	ConfigValidFragment template.HTML
	Token               string
	UserNumber          int
}

type telegram struct {
	Token   string
	Targets []int64

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

	var config bytes.Buffer
	enc := gob.NewEncoder(&config)
	err := enc.Encode(t)
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
	}
	td.ConfigValidFragment = helper.ConfigInvalid
	if td.Valid {
		td.ConfigValidFragment = helper.ConfigValid
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

	remove := make(map[int64]bool)
	for i := range t.Targets {
		c, err := t.bot.ChatByID(strconv.FormatInt(t.Targets[i], 10))
		if err != nil {
			em := fmt.Sprintln("telegram:", err)
			log.Println(em)
			t.e <- em
			remove[t.Targets[i]] = true
			continue
		}
		_, err = t.bot.Send(c, a.Message, telebot.NoPreview)
		if err != nil {
			em := fmt.Sprintln("telegram:", err)
			log.Println(em)
			t.e <- em
			remove[t.Targets[i]] = true
			continue
		}
	}

	// cleanup
	if len(remove) != 0 {
		newIDs := make([]int64, 0, len(t.Targets))
		for i := range t.Targets {
			if !remove[t.Targets[i]] {
				newIDs = append(newIDs, t.Targets[i])
			}
		}
		t.Targets = newIDs
		t.update()
	}
}
