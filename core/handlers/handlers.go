package handlers

import (
	"bytes"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
)

type TemplateRenderer struct {
	templatesDir string
}

func NewTemplateRenderer(templatesDir string) *TemplateRenderer {
	return &TemplateRenderer{templatesDir: templatesDir}
}

func (tr *TemplateRenderer) Render(filename string, data interface{}) (string, error) {
	path := filepath.Join(tr.templatesDir, filename)
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		log.Printf("Template parse error (%s): %v", filename, err)
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		log.Printf("Template execute error (%s): %v", filename, err)
		return "", err
	}

	return buf.String(), nil
}

func (tr *TemplateRenderer) RenderFile(filename string, data interface{}, w http.ResponseWriter) error {
	path := filepath.Join(tr.templatesDir, filename)
	tmpl, err := template.ParseFiles(path)
	if err != nil {
		log.Printf("Template parse error (%s): %v", filename, err)
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("Template execute error (%s): %v", filename, err)
		return err
	}

	return nil
}
