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

func TestRootCommandIncludesDiagnosticsCollect(t *testing.T) {
	root := NewRootCommand()
	command, _, err := root.Find([]string{"diagnostics", "collect"})
	if err != nil {
		t.Fatalf("Find diagnostics collect: %v", err)
	}
	if command == nil || command.Use != "collect <agent-id>" {
		t.Fatalf("command = %#v, want diagnostics collect", command)
	}
}

func TestDiagnosticsCollectValidatesBeforeNetwork(t *testing.T) {
	cmd := newDiagnosticsCollectCommand(func() string { return "http://127.0.0.1:1" })
	cmd.SetArgs([]string{"agent-a", "--since", "not-a-timestamp"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected invalid --since to fail before making a request")
	}

	cmd = newDiagnosticsCollectCommand(func() string { return "http://127.0.0.1:1" })
	cmd.SetArgs([]string{"agent-a", "--limit", "1001"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected out-of-range --limit to fail before making a request")
	}
}
