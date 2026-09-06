package web

import (
	"embed"
	"html/template"
)

//go:embed templates/*.tmpl
var files embed.FS

func ParseTemplates() (*template.Template, error) {
	functions := template.FuncMap{"add": func(a, b int) int { return a + b }, "sub": func(a, b int) int { return a - b }}
	return template.New("root").Funcs(functions).ParseFS(files, "templates/*.tmpl")
}
