package utils

import (
	"html/template"
	"log"
	"net/http"
)

type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	Status  int    `json:"status"`
}

func RenderError(w http.ResponseWriter, status int, title, message string) {
	w.WriteHeader(status)
	tmpl, err := template.New("error").Parse(`
<!DOCTYPE html>
<html>
<head>
	<title>Error</title>
	<link rel="stylesheet" href="/static/css/style.css">
</head>
<body>
	<div class="container">
		<h1>{{.Title}}</h1>
		<p>{{.Message}}</p>
		<a href="/">Volver al inicio</a>
	</div>
</body>
</html>
`)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	tmpl.Execute(w, map[string]string{
		"Title":   title,
		"Message": message,
	})
}

func HandleError(w http.ResponseWriter, err error, context string) {
	log.Printf("[ERROR] %s: %v", context, err)
	RenderError(w, http.StatusInternalServerError, "Error", "Algo salió mal. Por favor intenta de nuevo.")
}

func BadRequest(w http.ResponseWriter, message string) {
	RenderError(w, http.StatusBadRequest, "Bad Request", message)
}

func Unauthorized(w http.ResponseWriter) {
	RenderError(w, http.StatusUnauthorized, "Unauthorized", "Por favor inicia sesión.")
}

func NotFound(w http.ResponseWriter) {
	RenderError(w, http.StatusNotFound, "Not Found", "La página que buscas no existe.")
}
