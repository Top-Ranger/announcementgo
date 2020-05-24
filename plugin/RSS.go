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
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/server"
	"github.com/gorilla/feeds"
)

func init() {
	err := registry.RegisterPlugin(rssFactory, "RSS")
	if err != nil {
		panic(err)
	}
}

func rssFactory(key, shortDescription string, errorChannel chan string) (registry.Plugin, error) {
	r := new(rss)
	b, err := registry.CurrentDataSafe.GetConfig(key, "RSS")
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
	r.key, r.shortDescription = key, shortDescription

	server.AddHandle(r.key, "RSS/feed.rss", func(rw http.ResponseWriter, req *http.Request) {
		r.l.Lock()
		defer r.l.Unlock()
		rw.Write(r.Cache)
	})

	r.e = errorChannel

	return r, nil
}

type rss struct {
	NumberShown int
	Cache       []byte
	Link        string

	l                     *sync.Mutex
	key, shortDescription string
	e                     chan string
}

func (r *rss) GetConfig() template.HTML {
	counter.StartProcess()
	defer counter.EndProcess()

	config := `
	<h1>RSS</h1>
	<p id="RSS_path"></p>
	<form method="POST">
	<input type="hidden" name="target" value="RSS">
	<p><label for="RSS_items">Number Items: </label><input type="number" id="RSS_items" name="items" min="0" step="1" value="%d" required></p>
	<p><label for="RSS_link">Link:</label> <input type="text" id="RSS_link" name="link" value="%s"></p>
	<p><input type="submit" value="Update"></p>
	</form>

	<script>
	var link = document.createElement("A");
	link.href = document.location.protocol + document.location.pathname +  "/RSS/feed.rss";
	link.textContent = link.href;
	var t = document.getElementById("RSS_path");
	t.appendChild(link)
	</script>
	`
	config = fmt.Sprintf(config, r.NumberShown, template.HTMLEscapeString(r.Link))
	return template.HTML(config)
}

func (r *rss) ProcessConfigChange(req *http.Request) error {
	counter.StartProcess()
	defer counter.EndProcess()
	err := req.ParseForm()
	if err != nil {
		return err
	}

	num := req.Form.Get("items")
	if num == "" {
		return nil
	}

	i, err := strconv.Atoi(num)
	if err != nil {
		return err
	}

	if i < 0 {
		return fmt.Errorf("number %d is smaller than 0", i)
	}

	r.Link = req.Form.Get("link")

	r.l.Lock()
	r.NumberShown = i
	r.l.Unlock()
	go r.update()
	return nil
}

func (r *rss) NewAnnouncement(a registry.Announcement, id string) {
	counter.StartProcess()
	defer counter.EndProcess()
	// a and id are not used, get the announcements directly from data safe
	r.update()
}

func (r *rss) update() {
	counter.StartProcess()
	defer counter.EndProcess()

	an, err := registry.CurrentDataSafe.GetAllAnnouncements(r.key)

	if err != nil {
		em := fmt.Sprintln("rss:", err)
		log.Println(em)
		r.e <- em
		// Try again later
		go func() {
			time.Sleep(15 * time.Minute)
			r.NewAnnouncement(registry.Announcement{}, "")
		}()
	}

	r.l.Lock()
	defer r.l.Unlock()
	if r.NumberShown != 0 {
		start := len(an) - r.NumberShown
		if start < 0 {
			start = 0
		}
		an = an[start:]
	}

	feed := &feeds.Feed{
		Title:       r.shortDescription,
		Description: r.shortDescription,
		Link:        &feeds.Link{Href: r.Link},
		Author:      &feeds.Author{Name: "AnnouncementGo!"},
		Updated:     time.Now(),
	}

	for i := 0; i < len(an); i++ {
		feed.Items = append(feed.Items, &feeds.Item{
			Title:       an[i].Header,
			Description: an[i].Message,
			Link:        &feeds.Link{},
			Created:     an[i].Time,
		})
	}

	data, err := feed.ToRss()
	if err != nil {
		log.Println("feed:", err)
	}
	r.Cache = []byte(data)

	var config bytes.Buffer
	enc := gob.NewEncoder(&config)
	err = enc.Encode(r)
	if err != nil {
		em := fmt.Sprintln("rss:", err)
		log.Println(em)
		r.e <- em
	}
	err = registry.CurrentDataSafe.SetConfig(r.key, "RSS", config.Bytes())
	if err != nil {
		em := fmt.Sprintln("rss:", err)
		log.Println(em)
		r.e <- em
	}
}
