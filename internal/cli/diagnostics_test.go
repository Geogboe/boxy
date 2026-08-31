package cli

import "testing"

func TestRootCommandIncludesDiagnosticsLogs(t *testing.T) {
	root := NewRootCommand()
	command, _, err := root.Find([]string{"diagnostics", "logs"})
	if err != nil {
		t.Fatalf("Find diagnostics logs: %v", err)
	}
	if command == nil || command.Use != "logs" {
		t.Fatalf("command = %#v, want diagnostics logs", command)
	}
}
