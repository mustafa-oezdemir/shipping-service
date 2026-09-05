package web

import (
	"embed"
	"html/template"
)

//go:embed templates/*.tmpl
var files embed.FS

func ParseTemplates() (*template.Template, error) {
	return template.New("root").ParseFS(files, "templates/*.tmpl")
}
