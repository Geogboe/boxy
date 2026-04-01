package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// mockUpdater stubs updaterIface for unit tests.
type mockUpdater struct {
	latestVersion string
	latestErr     error
	installErr    error
	installedPath string
}

func (m *mockUpdater) CheckLatest(_ context.Context) (string, error) {
	return m.latestVersion, m.latestErr
}

func (m *mockUpdater) Install(_ context.Context, _, exePath string) error {
	m.installedPath = exePath
	return m.installErr
}

// newTestUpdateCmd returns a cobra.Command wired for testing runUpdate directly.
func newTestUpdateCmd(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd
}

// withMockUpdater replaces updateNewUpdater for the duration of a test.
func withMockUpdater(t *testing.T, mock *mockUpdater) {
	t.Helper()
	orig := updateNewUpdater
	updateNewUpdater = func(_ updateOptions) updaterIface { return mock }
	t.Cleanup(func() { updateNewUpdater = orig })
}

// withVersion temporarily sets cli.Version for a test.
func withVersion(t *testing.T, v string) {
	t.Helper()
	orig := Version
	Version = v
	t.Cleanup(func() { Version = orig })
}

func TestRunUpdate_AlreadyUpToDate(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "v1.0.0"})

	var out bytes.Buffer
	if err := runUpdate(newTestUpdateCmd(&out), updateOptions{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out.String(), "Already up to date") {
		t.Errorf("expected 'Already up to date' in output, got:\n%s", out.String())
	}
}

func TestRunUpdate_AlreadyUpToDate_StripsVPrefix(t *testing.T) {
	// "v1.0.0" current vs "1.0.0" latest (or vice versa) should be treated equal.
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "1.0.0"})

	var out bytes.Buffer
	if err := runUpdate(newTestUpdateCmd(&out), updateOptions{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out.String(), "Already up to date") {
		t.Errorf("expected 'Already up to date' in output, got:\n%s", out.String())
	}
}

func TestRunUpdate_CheckOnly_UpdateAvailable(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "v1.1.0"})

	var out bytes.Buffer
	err := runUpdate(newTestUpdateCmd(&out), updateOptions{checkOnly: true})
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out.String(), "update available") {
		t.Errorf("expected 'update available' in output, got:\n%s", out.String())
	}
}

func TestRunUpdate_InstallsToExePath(t *testing.T) {
	withVersion(t, "v1.0.0")
	mock := &mockUpdater{latestVersion: "v1.1.0"}
	withMockUpdater(t, mock)
	t.Setenv("BOXY_TEST_EXE_PATH", t.TempDir()+"/boxy")

	var out bytes.Buffer
	if err := runUpdate(newTestUpdateCmd(&out), updateOptions{}); err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if !strings.Contains(out.String(), "updated to") {
		t.Errorf("expected 'updated to' in output, got:\n%s", out.String())
	}
	if mock.installedPath == "" {
		t.Error("expected Install to be called with an exe path")
	}
}

func TestRunUpdate_InstallError(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{
		latestVersion: "v1.1.0",
		installErr:    errors.New("disk full"),
	})
	t.Setenv("BOXY_TEST_EXE_PATH", t.TempDir()+"/boxy")

	var out bytes.Buffer
	err := runUpdate(newTestUpdateCmd(&out), updateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "install") {
		t.Errorf("expected 'install' in error, got: %v", err)
	}
}

func TestRunUpdate_CheckLatestError(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestErr: errors.New("network error")})

	var out bytes.Buffer
	err := runUpdate(newTestUpdateCmd(&out), updateOptions{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "check for updates") {
		t.Errorf("expected 'check for updates' in error, got: %v", err)
	}
}

func TestUpdateCommand_FlagsWireCorrectly(t *testing.T) {
	withVersion(t, "v1.0.0")

	var capturedOpts updateOptions
	orig := updateNewUpdater
	updateNewUpdater = func(opts updateOptions) updaterIface {
		capturedOpts = opts
		return &mockUpdater{latestVersion: "v1.0.0"}
	}
	t.Cleanup(func() { updateNewUpdater = orig })

	cmd := newUpdateCommand()
	cmd.SetArgs([]string{"--check", "--version", "v0.9.0", "--proxy", "http://proxy:8080"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !capturedOpts.checkOnly {
		t.Error("expected checkOnly=true")
	}
	if capturedOpts.pinnedVersion != "v0.9.0" {
		t.Errorf("expected pinnedVersion='v0.9.0', got %q", capturedOpts.pinnedVersion)
	}
	if capturedOpts.proxyURL != "http://proxy:8080" {
		t.Errorf("expected proxyURL='http://proxy:8080', got %q", capturedOpts.proxyURL)
	}
}

func TestUpdateCommand_TokenFromEnv(t *testing.T) {
	withVersion(t, "v1.0.0")
	t.Setenv("BOXY_GITHUB_TOKEN", "my-secret-token")

	var capturedOpts updateOptions
	orig := updateNewUpdater
	updateNewUpdater = func(opts updateOptions) updaterIface {
		capturedOpts = opts
		return &mockUpdater{latestVersion: "v1.0.0"}
	}
	t.Cleanup(func() { updateNewUpdater = orig })

	cmd := newUpdateCommand()
	cmd.SetArgs([]string{})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	_ = cmd.Execute()

	if capturedOpts.token != "my-secret-token" {
		t.Errorf("expected token 'my-secret-token', got %q", capturedOpts.token)
	}
}
