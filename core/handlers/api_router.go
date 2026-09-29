package handlers

import (
	"net/http"
	"strings"
)

func HandleAPIRoute(w http.ResponseWriter, r *http.Request, crudHandler *GenericCRUDHandler) {
	// La ruta base es /api/, deje el resto en path.
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	segments := strings.Split(strings.Trim(path, "/"), "/")

	if len(segments) == 0 || segments[0] == "" {
		http.NotFound(w, r)
		return
	}

	params := PathParams{}

	if len(segments) >= 1 {
		params["module"] = segments[0]
	}
	if len(segments) >= 2 {
		params["model"] = segments[1]
	}
	if len(segments) >= 3 {
		params["id"] = segments[2]
	}

	if len(segments) == 4 && segments[3] == "transition" {
		r = WithPathParams(r, params)
		crudHandler.HandleTransition(w, r)
		return
	}
	if len(segments) == 4 && segments[3] == "history" {
		r = WithPathParams(r, params)
		crudHandler.HandleHistory(w, r)
		return
	}

	if len(segments) >= 2 {
		r = WithPathParams(r, params)
		crudHandler.HandleCRUD(w, r)
		return
	}

	http.NotFound(w, r)
}
