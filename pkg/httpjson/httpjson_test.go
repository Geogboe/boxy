package httpjson_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Geogboe/boxy/v2/pkg/httpjson"
)

func TestWrite(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	httpjson.Write(w, http.StatusOK, map[string]string{"hello": "world"})

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}

	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body["hello"] != "world" {
		t.Fatalf("body = %v", body)
	}
}

func TestWrite_slice(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	httpjson.Write(w, http.StatusOK, []string{"a", "b"})

	var body []string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body) != 2 || body[0] != "a" {
		t.Fatalf("body = %v", body)
	}
}

func TestError(t *testing.T) {
	t.Parallel()

	w := httptest.NewRecorder()
	httpjson.Error(w, http.StatusNotFound, "not found")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusNotFound)
	}

	var body struct{ Error string }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error != "not found" {
		t.Fatalf("error = %q, want %q", body.Error, "not found")
	}
}

func TestContentType_middleware(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	httpjson.ContentType(inner).ServeHTTP(w, r)

	if ct := w.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("content-type = %q", ct)
	}
}
