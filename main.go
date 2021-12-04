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
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/Top-Ranger/announcementgo/counter"
	_ "github.com/Top-Ranger/announcementgo/datasafe"
	_ "github.com/Top-Ranger/announcementgo/passwordmethods"
	_ "github.com/Top-Ranger/announcementgo/plugin"
	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/server"
	"github.com/Top-Ranger/announcementgo/translation"
)

// ConfigStruct contains all configuration options for PollGo!
type ConfigStruct struct {
	Language            string
	Address             string
	LogFailedLogin      bool
	LoginMinutes        int
	PathConfig          string
	PathImpressum       string
	PathDSGVO           string
	AnnouncementsFolder string
	DataSafe            string
	DataSafeConfig      string
}

var config ConfigStruct

func loadConfig(path string) (ConfigStruct, error) {
	log.Printf("main: Loading config (%s)", path)
	b, err := os.ReadFile(path)
	if err != nil {
		return ConfigStruct{}, errors.New(fmt.Sprintln("Can not read config.json:", err))
	}

	c := ConfigStruct{}
	err = json.Unmarshal(b, &c)
	if err != nil {
		return ConfigStruct{}, errors.New(fmt.Sprintln("Error while parsing config.json:", err))
	}

	return c, nil
}

func main() {
	configPath := flag.String("config", "./config.json", "Path to json config for AnnouncementGo!")
	dumpAnnouncements := flag.String("dumpAnnouncements", "", "If set, all anouncements of the provided key will be dumped to stdout")
	insertAnnouncements := flag.String("insertAnnouncements", "", "If set, announcements are read from stdin and directly inserted for the provided key (announcements will not be send)")
	flag.Parse()

	c, err := loadConfig(*configPath)
	if err != nil {
		panic(err)
	}
	config = c

	err = translation.SetDefaultTranslation(config.Language)
	if err != nil {
		log.Panicf("main: Error setting default language '%s': %s", config.Language, err.Error())
	}
	log.Printf("main: Setting language to '%s'", config.Language)

	datasafe, ok := registry.GetDataSafe(config.DataSafe)
	if !ok {
		log.Panicf("main: Unknown data safe %s", config.DataSafe)
	}

	b, err := os.ReadFile(config.DataSafeConfig)
	if err != nil {
		log.Panicln(err)
	}

	err = datasafe.InitialiseDatasafe(b)
	if err != nil {
		log.Panicln(err)
	}

	registry.CurrentDataSafe = datasafe

	if *dumpAnnouncements != "" {
		a, err := datasafe.GetAllAnnouncements(*dumpAnnouncements)
		if err != nil {
			log.Println("Can not read announcements for dump:", err)
			return
		}
		e := json.NewEncoder(os.Stdout)
		e.SetIndent("", "\t")
		err = e.Encode(a)
		if err != nil {
			log.Println("Can not encode announcements for dump:", err)
			return
		}
		return
	}

	if *insertAnnouncements != "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			log.Println("Can not read announcements for insert:", err)
			return
		}
		var a []registry.Announcement
		err = json.Unmarshal(b, &a)
		if err != nil {
			log.Println("Can not decode announcements for insert:", err)
			return
		}
		for i := range a {
			_, err := datasafe.SaveAnnouncement(*insertAnnouncements, a[i])
			if err != nil {
				log.Printf("Can not save %+v to dataset (will ignore it): %s", a[i], err)
			}
		}
		return
	}

	err = server.InitialiseServer(server.Config{Address: config.Address, PathDSGVO: config.PathDSGVO, PathImpressum: config.PathImpressum, CookieTimeMinute: config.LoginMinutes})
	if err != nil {
		log.Panicln(err)
	}

	err = LoadAnnouncements(config.PathConfig)
	if err != nil {
		log.Panicln(err)
	}

	server.RunServer()

	s := make(chan os.Signal, 1)
	signal.Notify(s, os.Interrupt, syscall.SIGTERM)

	log.Println("main: waiting")

	for range s {
		server.StopServer()
		counter.WaitProcesses()
		return
	}
}
