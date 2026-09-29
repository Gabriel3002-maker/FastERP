package handlers

import (
	"context"
	"net/http"
)

type pathParamsKeyType struct{}

var pathParamsKey = pathParamsKeyType{}

type PathParams map[string]string

func WithPathParams(r *http.Request, params PathParams) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), pathParamsKey, params))
}

func PathValue(r *http.Request, name string) string {
	params, ok := r.Context().Value(pathParamsKey).(PathParams)
	if !ok {
		return ""
	}
	return params[name]
}
