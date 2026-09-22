package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/sandbox"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/store"
)

func TestSandboxNotificationsFollowDurableRequests(t *testing.T) {
	st := store.NewMemoryStore()
	calls := 0
	var id model.SandboxID
	s := NewWithOptions(st, sandbox.New(st, nil), nil, nil, "", false, ServerOptions{NotifyWork: func() {
		calls++
		items, err := st.ListSandboxes(context.Background())
		if err != nil || len(items) != 1 {
			t.Fatal("notification before durable request")
		}
		id = items[0].ID
		if calls == 1 && items[0].Status != model.SandboxStatusPending {
			t.Fatal("create state not pending")
		}
		if calls == 2 && items[0].Status == model.SandboxStatusPending {
			t.Fatal("delete state not persisted")
		}
	}})
	w := httptest.NewRecorder()
	s.handleCreateSandbox(w, httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(`{"name":"boxy-test","requests":[{"type":"container","profile":"alpine","count":1}]}`)))
	if w.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("create status=%d notifications=%d", w.Code, calls)
	}
	r := httptest.NewRequest(http.MethodDelete, "/api/v1/sandboxes/test", nil)
	r.SetPathValue("id", string(id))
	w = httptest.NewRecorder()
	s.handleDeleteSandbox(w, r)
	if w.Code != http.StatusAccepted || calls != 2 {
		t.Fatalf("delete status=%d notifications=%d", w.Code, calls)
	}
	w = httptest.NewRecorder()
	s.handleCreateSandbox(w, httptest.NewRequest(http.MethodPost, "/api/v1/sandboxes", strings.NewReader(`{}`)))
	if w.Code != http.StatusBadRequest || calls != 2 {
		t.Fatal("invalid request triggered notification")
	}
}
