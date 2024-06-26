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

	"github.com/Top-Ranger/announcementgo/counter"
	"github.com/Top-Ranger/announcementgo/helper"
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/translation"
	"github.com/bwmarrin/discordgo"
)

// create bot: https://discord.com/developers/applications

func init() {
	var err error
	discordConfigTemplate, err = template.New("discordConfigTemplate").Parse(discordConfig)
	if err != nil {
		panic(err)
	}

	err = registry.RegisterPlugin(discordFactory, "Discord")
	if err != nil {
		panic(err)
	}
}

func discordFactory(key, shortDescription string, errorChannel chan string) (registry.Plugin, error) {
	d := new(discord)
	b, err := registry.CurrentDataSafe.GetConfig(key, "Discord")
	if err != nil {
		return nil, err
	}
	if len(b) != 0 {
		buf := bytes.NewBuffer(b)
		dec := gob.NewDecoder(buf)
		err = dec.Decode(d)
		if err != nil {
			return nil, err
		}
		if d.Token != "" && d.TokenHidden {
			d.Token, err = helper.UnhidePassword(d.Token)
			if err != nil {
				return nil, err
			}
		}
	}
	d.l = new(sync.Mutex)
	d.key = key
	d.e = errorChannel

	d.l.Lock()
	defer d.l.Unlock()

	err = d.update()

	return d, err
}

const discordConfig = `
<h1>Discord</h1>
{{.ConfigValidFragment}}
{{if .URL}}
<p>{{.URL}}</p>
<p>Administrators of servers can mention the bot on a channel to tell the bot to use that channel for announcements.</p>
{{end}}
<p>{{.UserNumber}} users</p>
<form method="POST">
	<input type="hidden" name="target" value="Discord">
	<p><input id="Discord_token" type="text" name="token" value="{{.Token}}" placeholder="token"> <label for="Discord_token">Discord Bot API token</label></p>
	<p><input type="submit" value="Update"></p>
</form>
`

var discordConfigTemplate *template.Template

type discordConfigTemplateStruct struct {
	Valid               bool
	ConfigValidFragment template.HTML
	Token               string
	UserNumber          string
	URL                 string
}

type discord struct {
	Token       string
	TokenHidden bool
	Channels    map[string]string

	bot          *discordgo.Session
	currentToken string
	l            *sync.Mutex
	e            chan string
	key          string
}

func (d *discord) update() error {
	// Caller has to lock
	counter.StartProcess()
	defer counter.EndProcess()

	if d.currentToken != d.Token {
		if d.bot != nil {
			d.bot.Close()
			d.bot = nil
		}
	}

	if d.bot == nil && d.Token != "" {
		var err error
		d.bot, err = discordgo.New("Bot " + d.Token)
		if err != nil {
			d.bot = nil
			em := fmt.Sprintln("discord:", err)
			log.Println(em)
			d.e <- em
			return err
		}

		d.bot.AddHandler(func(s *discordgo.Session, m *discordgo.MessageCreate) {
			if m.Author.ID == s.State.User.ID {
				return
			}

			c, err := d.bot.Channel(m.ChannelID)
			if err != nil {
				em := fmt.Sprintln("discord:", err)
				log.Println(em)
				d.e <- em
				return
			}

			switch c.Type {
			case discordgo.ChannelTypeDM:
				d.bot.ChannelMessageSend(m.ChannelID, translation.GetDefaultTranslation().BotAnswerMessage)

			case discordgo.ChannelTypeGuildText, discordgo.ChannelTypeGuildNews:
				if m.Message.GuildID == "" || m.Message.ChannelID == "" {
					return
				}

				mentioned := false

				for i := range m.Message.Mentions {
					if m.Message.Mentions[i].ID == s.State.User.ID {
						mentioned = true
						break
					}
				}

				if !mentioned {
					return
				}

				member, err := s.GuildMember(m.Message.GuildID, m.Message.Author.ID)
				if err != nil {
					em := fmt.Sprintln("discord:", err)
					log.Println(em)
					d.e <- em
					return
				}

				isAdmin := false
				for mr := range member.Roles {
					if role, err := s.State.Role(m.Message.GuildID, member.Roles[mr]); err == nil {
						isAdmin = (role.Permissions & discordgo.PermissionAdministrator) != 0
						if isAdmin {
							break
						}
					}
				}

				if !isAdmin {
					return
				}

				d.l.Lock()
				defer d.l.Unlock()
				if d.Channels == nil {
					d.Channels = make(map[string]string)
				}
				d.Channels[m.Message.GuildID] = m.Message.ChannelID
				err = d.update()
				if err != nil {
					em := fmt.Sprintln("discord:", err)
					log.Println(em)
					d.e <- em
					return
				}

				_, err = d.bot.ChannelMessageSend(m.ChannelID, translation.GetDefaultTranslation().BotSendOnThisChannel)
				if err != nil {
					em := fmt.Sprintln("discord:", err)
					log.Println(em)
					d.e <- em
					return
				}
			}
		})

		d.currentToken = d.Token
		err = d.bot.Open()
		if err != nil {
			d.bot = nil
			em := fmt.Sprintln("discord:", err)
			log.Println(em)
			d.e <- em
			return err
		}
	}

	tmpToken := d.Token
	d.TokenHidden = true
	d.Token = helper.HidePassword(d.Token)
	var config bytes.Buffer
	enc := gob.NewEncoder(&config)
	err := enc.Encode(d)
	d.Token = tmpToken
	d.TokenHidden = false
	if err != nil {
		em := fmt.Sprintln("discord:", err)
		log.Println(em)
		d.e <- em
		return err
	}
	err = registry.CurrentDataSafe.SetConfig(d.key, "Discord", config.Bytes())
	if err != nil {
		em := fmt.Sprintln("discord:", err)
		log.Println(em)
		d.e <- em
		return err
	}

	return nil
}

func (d *discord) GetConfig() template.HTML {
	counter.StartProcess()
	defer counter.EndProcess()
	d.l.Lock()
	defer d.l.Unlock()

	td := discordConfigTemplateStruct{
		Valid:      d.bot != nil,
		Token:      d.Token,
		UserNumber: "[not available]",
		URL:        "Create bot: https://discord.com/developers/applications",
	}

	if d.bot != nil {
		a, err := d.bot.Application("@me")
		if err != nil {
			em := fmt.Sprintln("discord:", err)
			log.Println(em)
			d.e <- em
		} else {
			td.URL = fmt.Sprintf("https://discord.com/api/oauth2/authorize?client_id=%s&scope=bot&permissions=2048", url.QueryEscape(a.ID))
		}
		g, err := d.bot.UserGuilds(100, "", "", false)
		if err != nil {
			em := fmt.Sprintln("discord:", err)
			log.Println(em)
			d.e <- em
			td.UserNumber = em
		} else {
			td.UserNumber = strconv.Itoa(len(g))
			if len(g) >= 100 {
				td.UserNumber = "100+"
			}
		}
	}

	td.ConfigValidFragment = helper.ConfigInvalid
	if td.Valid {
		td.ConfigValidFragment = helper.ConfigValid
	}
	var buf bytes.Buffer
	err := discordConfigTemplate.Execute(&buf, td)
	if err != nil {
		log.Printf("discord (%s): %s", d.key, err.Error())
	}
	return template.HTML(buf.String())

}

func (d *discord) ProcessConfigChange(r *http.Request) error {
	counter.StartProcess()
	defer counter.EndProcess()
	err := r.ParseForm()
	if err != nil {
		return err
	}

	d.l.Lock()
	defer d.l.Unlock()

	d.Token = r.Form.Get("token")

	err = d.update()
	if err != nil {
		em := fmt.Sprintln("discord:", err)
		log.Println(em)
		d.e <- em
		return err
	}

	return nil
}

func (d *discord) NewAnnouncement(a registry.Announcement, id string) {
	counter.StartProcess()
	defer counter.EndProcess()
	d.l.Lock()
	defer d.l.Unlock()

	if d.bot == nil {
		// no bot configurated - jump out
		return
	}

	// used in send
	message := strings.Join([]string{a.Header, a.Message}, "\n\n")

	send := func(channelID string) error {
		// caller has to lock
		if len(message) > 1500 { // discord character limit
			// We probably need to send a file
			buf := bytes.NewBufferString(message)
			_, err := d.bot.ChannelFileSend(channelID, strings.Join([]string{d.key, "txt"}, "."), buf)
			return err
		}
		// We can send the string
		_, err := d.bot.ChannelMessageSend(channelID, message)
		return err
	}

	startid := ""
	loop := true

	for loop {
		guilds, err := d.bot.UserGuilds(100, "", startid, false)
		if err != nil {
			em := fmt.Sprintln("discord:", err)
			log.Println(em)
			d.e <- em
			break
		}

		loop = len(guilds) == 100
		startid = guilds[len(guilds)-1].ID

		for g := range guilds {
			c, err := d.bot.GuildChannels(guilds[g].ID)
			if err != nil {
				em := fmt.Sprintln("discord:", err)
				log.Println(em)
				d.e <- em
				continue
			}

			messageSent := false

			// Try to send on predetermined channel
			if d.Channels[guilds[g].ID] != "" {
				channelID := d.Channels[guilds[g].ID]
				for i := range c {
					if c[i].ID == channelID {
						// We found the channel
						err = send(c[i].ID)
						if err != nil {
							em := fmt.Sprintln("discord:", err)
							log.Println(em)
							d.e <- em
							continue
						}
						messageSent = true
						break
					}
				}
			}

			// Try to post in announcement channel
			if !messageSent {
				for i := range c {
					if c[i].Type == discordgo.ChannelTypeGuildNews {
						err = send(c[i].ID)
						if err != nil {
							em := fmt.Sprintln("discord:", err)
							log.Println(em)
							d.e <- em
							continue
						}
						messageSent = true
						break
					}
				}
			}

			// Ok, there is none. Try text channels instead
			if !messageSent {
				for i := range c {
					if c[i].Type == discordgo.ChannelTypeGuildText {
						err = send(c[i].ID)
						if err != nil {
							em := fmt.Sprintln("discord:", err)
							log.Println(em)
							d.e <- em
						}
						messageSent = true
						break
					}
				}
			}
		}
	}
}
