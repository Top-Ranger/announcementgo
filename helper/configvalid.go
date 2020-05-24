package helper

import "html/template"

// ConfigValid holds a HTML fragment which can be displayed when the plugin configuration is valid.
// Use this to get a consistent look.
const ConfigValid template.HTML = `<h1 style="color: green;">&#9745; configuration valid</h1>`

// ConfigInvalid holds a HTML fragment which can be displayed when the plugin configuration is not valid.
// Use this to get a consistent look.
const ConfigInvalid template.HTML = `<h1 style="color: red;">&#9746; configuration not valid</h1>`
