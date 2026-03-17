// Package httpjson provides generic JSON HTTP response helpers.
//
// It keeps handler code clean by encapsulating the marshal → set Content-Type →
// write status pattern that every JSON API endpoint repeats.
package httpjson

import (
	"encoding/json"
	"net/http"
)

const contentType = "application/json; charset=utf-8"

// Write marshals data as JSON and writes it with the given HTTP status code.
// If marshalling fails, it falls back to a 500 error response.
func Write(w http.ResponseWriter, status int, data any) {
	b, err := json.Marshal(data)
	if err != nil {
		Error(w, http.StatusInternalServerError, "internal error")
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// errorBody is the JSON structure for error responses.
type errorBody struct {
	Error string `json:"error"`
}

// Error writes a JSON error response with the given HTTP status code.
func Error(w http.ResponseWriter, status int, message string) {
	b, _ := json.Marshal(errorBody{Error: message})
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// ContentType is middleware that sets the Content-Type header to application/json.
func ContentType(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		next.ServeHTTP(w, r)
	})
}
