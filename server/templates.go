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

package server

import (
	"html/template"

	"github.com/Top-Ranger/announcementgo/registry"
	"github.com/Top-Ranger/announcementgo/translation"
)

// LoginTemplate contains the template for the login page.
var LoginTemplate *template.Template

// AnnouncementTemplate contains the template for the main page.
var AnnouncementTemplate *template.Template

// HistoryTemplate contains the template for the history page.
var HistoryTemplate *template.Template

// LoginTemplateStruct is a struct for the LoginTemplate.
type LoginTemplateStruct struct {
	Key              string
	ShortDescription string
	Translation      translation.Translation
}

// AnnouncementTemplateStruct is a struct for the AnnouncementTemplate.
type AnnouncementTemplateStruct struct {
	Key              string
	Admin            bool
	Message          string
	ShortDescription string
	PluginConfig     []template.HTML
	Translation      translation.Translation
	Errors           []string
}

// HistoryTemplateStruct is a struct for the HistoryTemplate.
type HistoryTemplateStruct struct {
	Key              string
	ShortDescription string
	History          []registry.Announcement
	Translation      translation.Translation
}

func init() {
	funcMap := template.FuncMap{
		"even": func(i int) bool {
			return i%2 == 0
		},
	}

	b, err := templateFiles.ReadFile("template/login.html")
	if err != nil {
		panic(err)
	}
	LoginTemplate, err = template.New("login").Parse(string(b))
	if err != nil {
		panic(err)
	}

	b, err = templateFiles.ReadFile("template/announcement.html")
	if err != nil {
		panic(err)
	}
	AnnouncementTemplate, err = template.New("announcement").Funcs(funcMap).Parse(string(b))
	if err != nil {
		panic(err)
	}

	b, err = templateFiles.ReadFile("template/history.html")
	if err != nil {
		panic(err)
	}
	HistoryTemplate, err = template.New("history").Funcs(funcMap).Parse(string(b))
	if err != nil {
		panic(err)
	}
}
