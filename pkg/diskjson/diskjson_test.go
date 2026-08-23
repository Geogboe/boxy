package diskjson

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

type widget struct {
	Count int      `json:"count"`
	Names []string `json:"names"`
}

func TestStore_Load_MissingFile_ReturnsZeroValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New[widget](path, nil)

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Count != 0 || got.Names != nil {
		t.Fatalf("Load() on missing file = %+v, want zero value", got)
	}
}

func TestStore_Load_MissingFile_UsesNewFunc(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New(path, func() widget { return widget{Count: 7, Names: []string{"seed"}} })

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := widget{Count: 7, Names: []string{"seed"}}
	if got.Count != want.Count || len(got.Names) != 1 || got.Names[0] != "seed" {
		t.Fatalf("Load() on missing file = %+v, want %+v", got, want)
	}
}

func TestStore_SaveThenLoad_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New[widget](path, nil)

	want := widget{Count: 3, Names: []string{"a", "b"}}
	if err := s.Save(want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Count != want.Count || len(got.Names) != len(want.Names) {
		t.Fatalf("Load() after Save() = %+v, want %+v", got, want)
	}
}

func TestStore_Save_CreatesParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "widget.json")
	s := New[widget](path, nil)

	if err := s.Save(widget{Count: 1}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file to exist at %q: %v", path, err)
	}
}

func TestStore_Save_DoesNotLeaveTmpFileBehind(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "widget.json")
	s := New[widget](path, nil)

	if err := s.Save(widget{Count: 1}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected .tmp file to be gone after Save(), stat error = %v", err)
	}
}

func TestStore_Save_FilePermissionsAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningfully enforced on Windows")
	}
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New[widget](path, nil)

	if err := s.Save(widget{Count: 1}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file permissions = %v, want 0600", perm)
	}
}

func TestStore_Update_MutatesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New[widget](path, nil)

	if _, err := s.Update(func(w widget) (widget, error) {
		w.Count++
		w.Names = append(w.Names, "x")
		return w, nil
	}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Count != 1 || len(got.Names) != 1 || got.Names[0] != "x" {
		t.Fatalf("Load() after Update() = %+v", got)
	}
}

func TestStore_Update_StartsFromNewFuncWhenFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New(path, func() widget { return widget{Count: 10} })

	got, err := s.Update(func(w widget) (widget, error) {
		w.Count++
		return w, nil
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got.Count != 11 {
		t.Fatalf("Update() result = %+v, want Count=11", got)
	}
}

func TestStore_Update_FnError_DoesNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New[widget](path, nil)

	sentinel := errors.New("boom")
	if _, err := s.Update(func(w widget) (widget, error) {
		return w, sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("Update() error = %v, want %v", err, sentinel)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written after fn error, stat error = %v", err)
	}
}

func TestStore_Update_SerializesConcurrentCallers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New[widget](path, nil)

	const n = 50
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := s.Update(func(w widget) (widget, error) {
				w.Count++
				return w, nil
			}); err != nil {
				t.Errorf("Update() error = %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := s.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.Count != n {
		t.Fatalf("Count after %d concurrent Update() calls = %d, want %d (lost update if lower)", n, got.Count, n)
	}
}

func TestStore_Path_ReturnsConfiguredPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "widget.json")
	s := New[widget](path, nil)
	if got := s.Path(); got != path {
		t.Fatalf("Path() = %q, want %q", got, path)
	}
}

func TestStore_Load_CorruptFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "widget.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatalf("seed corrupt file: %v", err)
	}
	s := New[widget](path, nil)

	if _, err := s.Load(); err == nil {
		t.Fatalf("Load() on corrupt file: want error, got nil")
	}
}
