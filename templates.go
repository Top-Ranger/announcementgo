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
	"html/template"
	"io/ioutil"

	"github.com/Top-Ranger/announcementgo/translation"
)

var loginTemplate *template.Template
var announcementTemplate *template.Template

type loginTemplateStruct struct {
	Key              string
	ShortDescription string
	Translation      translation.Translation
}

type announcementTemplateStruct struct {
	Key              string
	Admin            bool
	Message          string
	ShortDescription string
	PluginConfig     []template.HTML
	Translation      translation.Translation
}

func init() {
	funcMap := template.FuncMap{
		"even": func(i int) bool {
			return i%2 == 0
		},
	}

	b, err := ioutil.ReadFile("template/login.html")
	if err != nil {
		panic(err)
	}
	loginTemplate, err = template.New("login").Parse(string(b))
	if err != nil {
		panic(err)
	}

	b, err = ioutil.ReadFile("template/announcement.html")
	if err != nil {
		panic(err)
	}
	announcementTemplate, err = template.New("announcement").Funcs(funcMap).Parse(string(b))
	if err != nil {
		panic(err)
	}
}
