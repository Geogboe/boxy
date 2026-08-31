package scripts_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBugReportTemplateContainsTriageAndSafeReproFields(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", ".github", "ISSUE_TEMPLATE", "bug_report.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bug report template: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		`labels: ["bug", "triage"]`,
		"I searched existing issues for duplicates",
		"I removed or redacted API keys, passwords, tokens, hostnames, usernames",
		"Boxy version or commit",
		"Operating system / architecture",
		"Installation method",
		"Provider or agent",
		"Configuration shape",
		"## Reproduction steps",
		"## Expected behavior",
		"## Actual behavior",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("template missing %q", want)
		}
	}
}
