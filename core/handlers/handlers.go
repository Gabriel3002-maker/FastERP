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

func (tr *TemplateRenderer) RenderWithLayout(layoutFile, contentFile string, data map[string]interface{}, w http.ResponseWriter) error {
	// Render content file
	contentPath := filepath.Join(tr.templatesDir, contentFile)
	contentTmpl, err := template.ParseFiles(contentPath)
	if err != nil {
		log.Printf("Template parse error (%s): %v", contentFile, err)
		return err
	}

	var contentBuf bytes.Buffer
	if err := contentTmpl.Execute(&contentBuf, data); err != nil {
		log.Printf("Template execute error (%s): %v", contentFile, err)
		return err
	}

	// Add content to data
	if data == nil {
		data = make(map[string]interface{})
	}
	data["Content"] = template.HTML(contentBuf.String())
	data["ExtraStyles"] = template.CSS("")
	data["ExtraScripts"] = template.JS("")

	// Render layout with content
	layoutPath := filepath.Join(tr.templatesDir, layoutFile)
	layoutTmpl, err := template.ParseFiles(layoutPath)
	if err != nil {
		log.Printf("Template parse error (%s): %v", layoutFile, err)
		return err
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := layoutTmpl.Execute(w, data); err != nil {
		log.Printf("Template execute error (%s): %v", layoutFile, err)
		return err
	}

	return nil
}
