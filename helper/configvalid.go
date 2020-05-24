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

package helper

import "html/template"

// ConfigValid holds a HTML fragment which can be displayed when the plugin configuration is valid.
// Use this to get a consistent look.
const ConfigValid template.HTML = `<h1 style="color: green;">&#9745; configuration valid</h1>`

// ConfigInvalid holds a HTML fragment which can be displayed when the plugin configuration is not valid.
// Use this to get a consistent look.
const ConfigInvalid template.HTML = `<h1 style="color: red;">&#9746; configuration not valid</h1>`
