package scripts

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestGeneratedScriptsAreUpToDate runs `go generate` and verifies the
// committed install scripts match the generated output.  This catches
// cases where a template or constant changed but `go generate` was not
// re-run before committing.
func TestGeneratedScriptsAreUpToDate(t *testing.T) {
	root := repoRoot(t)
	scriptsDir := filepath.Join(root, "scripts")

	scripts := []string{"install.sh", "install.ps1"}
	before := make(map[string][]byte, len(scripts))
	for _, name := range scripts {
		data, err := os.ReadFile(filepath.Join(scriptsDir, name))
		if err != nil {
			t.Fatalf("read committed %s: %v", name, err)
		}
		before[name] = data
	}

	cmd := exec.Command("go", "generate", "./scripts/...")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go generate failed: %v\n%s", err, string(out))
	}

	for _, name := range scripts {
		after, err := os.ReadFile(filepath.Join(scriptsDir, name))
		if err != nil {
			t.Fatalf("read generated %s: %v", name, err)
		}
		// Normalize line endings so the comparison works regardless of
		// git's core.autocrlf setting on Windows.
		norm := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
		if !bytes.Equal(norm(before[name]), norm(after)) {
			// Restore the original file so the working tree stays clean.
			_ = os.WriteFile(filepath.Join(scriptsDir, name), before[name], 0o644)
			t.Errorf("%s is stale — run 'go generate ./scripts/...' and commit the result", name)
		}
	}
}
