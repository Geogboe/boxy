package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/Geogboe/rog/pkg/selfupdate"
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
	withFakeSvcManager(t, &fakeManager{}) // nothing installed: restart check no-ops
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

func TestUpdateCommand_PrereleaseFlag_WiresThrough(t *testing.T) {
	withVersion(t, "v1.0.0")

	var capturedOpts updateOptions
	orig := updateNewUpdater
	updateNewUpdater = func(opts updateOptions) updaterIface {
		capturedOpts = opts
		return &mockUpdater{latestVersion: "v1.0.0"}
	}
	t.Cleanup(func() { updateNewUpdater = orig })

	cmd := newUpdateCommand()
	cmd.SetArgs([]string{"--prerelease"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !capturedOpts.prerelease {
		t.Error("expected prerelease=true")
	}
}

func TestDefaultUpdateNewUpdater_SetsAllowPrereleaseAndDraft(t *testing.T) {
	updater := defaultUpdateNewUpdater(updateOptions{prerelease: true})
	bu, ok := updater.(*boxyUpdater)
	if !ok {
		t.Fatalf("expected *boxyUpdater, got %T", updater)
	}
	if !bu.u.AllowPrereleaseAndDraft {
		t.Error("expected underlying selfupdate.Updater.AllowPrereleaseAndDraft=true")
	}
}

func TestDefaultUpdateNewUpdater_DefaultsToStableOnly(t *testing.T) {
	updater := defaultUpdateNewUpdater(updateOptions{})
	bu, ok := updater.(*boxyUpdater)
	if !ok {
		t.Fatalf("expected *boxyUpdater, got %T", updater)
	}
	if bu.u.AllowPrereleaseAndDraft {
		t.Error("expected underlying selfupdate.Updater.AllowPrereleaseAndDraft=false by default")
	}
}

// githubAPIRewriteClient rewrites requests targeting the real GitHub API to a
// test server, mirroring the same trick used in rog's own selfupdate tests.
type githubAPIRewriteClient struct {
	base    *http.Client
	srvURL  string
	apiBase string
}

func (c *githubAPIRewriteClient) Do(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.String(), c.apiBase) {
		rewritten := c.srvURL + req.URL.Path
		if req.URL.RawQuery != "" {
			rewritten += "?" + req.URL.RawQuery
		}
		newURL, err := req.URL.Parse(rewritten)
		if err != nil {
			return nil, fmt.Errorf("rewrite test request URL: %w", err)
		}
		newReq := req.Clone(req.Context())
		newReq.URL = newURL
		req = newReq
	}
	return c.base.Do(req)
}

// TestBoxyUpdater_CheckLatest_NoStableRelease_MentionsPrereleaseFlag verifies
// that when every release is prerelease/draft-marked (so GitHub's
// /releases/latest 404s), boxy's own error wrapping points the user at
// --prerelease rather than surfacing rog's generic sentinel alone.
func TestBoxyUpdater_CheckLatest_NoStableRelease_MentionsPrereleaseFlag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	u := &selfupdate.Updater{
		Repo:       "Geogboe/boxy",
		BinaryName: "boxy",
		AssetNamer: func(v, goos, goarch string) string { return "boxy_" + v + "_" + goos + "_" + goarch },
		Client:     &githubAPIRewriteClient{base: srv.Client(), srvURL: srv.URL, apiBase: "https://api.github.com"},
	}
	bu := &boxyUpdater{u: u}

	_, err := bu.CheckLatest(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, selfupdate.ErrNoStableRelease) {
		t.Errorf("expected error to wrap selfupdate.ErrNoStableRelease, got: %v", err)
	}
	if !strings.Contains(err.Error(), "--prerelease") {
		t.Errorf("expected error to mention --prerelease, got: %v", err)
	}
}

func TestUpdateCommand_TokenFromEnv(t *testing.T) {
	withVersion(t, "v1.0.0")
	t.Setenv("BOXY_GITHUB_TOKEN", "${BOXY_TEST_GITHUB_TOKEN}")

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

	if capturedOpts.token != "${BOXY_TEST_GITHUB_TOKEN}" {
		t.Errorf("expected token '${BOXY_TEST_GITHUB_TOKEN}', got %q", capturedOpts.token)
	}
}

func TestRunUpdate_RefreshesCanonicalSkillAfterInstall(t *testing.T) {
	withVersion(t, "v1.0.0")
	mock := &mockUpdater{latestVersion: "v1.1.0"}
	withMockUpdater(t, mock)
	withFakeSvcManager(t, &fakeManager{}) // nothing installed: restart check no-ops

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config-home"))
	t.Setenv("BOXY_TEST_EXE_PATH", filepath.Join(t.TempDir(), "boxy"))

	var out bytes.Buffer
	if err := runUpdate(newTestUpdateCmd(&out), updateOptions{}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config-home", "boxy", "skills", "boxy-cli", "SKILL.md")); err != nil {
		t.Fatalf("expected canonical skill to be refreshed: %v", err)
	}
	versionData, err := os.ReadFile(filepath.Join(home, ".config-home", "boxy", "skills", "boxy-cli", ".boxy-skill-version"))
	if err != nil {
		t.Fatalf("ReadFile version: %v", err)
	}
	if strings.TrimSpace(string(versionData)) != "v1.1.0" {
		t.Fatalf("skill version = %q, want v1.1.0", strings.TrimSpace(string(versionData)))
	}
}

func TestRunUpdate_RestartsRunningInstalledService(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "v1.1.0"})
	t.Setenv("BOXY_TEST_EXE_PATH", t.TempDir()+"/boxy")

	system := &fakeManager{statusByName: map[string]svcmgr.Status{
		agentServiceName: {Installed: true, Running: true, Mode: "system-service"},
	}}
	user := &fakeManager{}
	withPerModeFakeSvcManager(t, system, user)

	var out bytes.Buffer
	if err := runUpdate(newTestUpdateCmd(&out), updateOptions{}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !slices.Contains(system.stoppedNames, agentServiceName) {
		t.Errorf("stoppedNames = %v, want to contain %q", system.stoppedNames, agentServiceName)
	}
	if !slices.Contains(system.startedNames, agentServiceName) {
		t.Errorf("startedNames = %v, want to contain %q", system.startedNames, agentServiceName)
	}
	if !strings.Contains(out.String(), "restarted "+agentServiceName) {
		t.Errorf("expected output to mention restarting %s, got:\n%s", agentServiceName, out.String())
	}
}

func TestRunUpdate_DoesNotRestartInstalledButStoppedService(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "v1.1.0"})
	t.Setenv("BOXY_TEST_EXE_PATH", t.TempDir()+"/boxy")

	system := &fakeManager{statusByName: map[string]svcmgr.Status{
		serveServiceName: {Installed: true, Running: false, Mode: "system-unit"},
	}}
	withFakeSvcManager(t, system)

	var out bytes.Buffer
	if err := runUpdate(newTestUpdateCmd(&out), updateOptions{}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if len(system.stoppedNames) != 0 || len(system.startedNames) != 0 {
		t.Errorf("a stopped (not running) service must not be started as a side effect of update; got stopped=%v started=%v", system.stoppedNames, system.startedNames)
	}
}

func TestRunUpdate_ChecksBothPrivilegedAndUserModeInstances(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "v1.1.0"})
	t.Setenv("BOXY_TEST_EXE_PATH", t.TempDir()+"/boxy")

	// boxy-agent installed privileged; boxy-serve installed --user. Both
	// running, both should be restarted via their respective manager.
	system := &fakeManager{statusByName: map[string]svcmgr.Status{
		agentServiceName: {Installed: true, Running: true, Mode: "system-service"},
	}}
	user := &fakeManager{statusByName: map[string]svcmgr.Status{
		serveServiceName: {Installed: true, Running: true, Mode: "user-task"},
	}}
	withPerModeFakeSvcManager(t, system, user)

	var out bytes.Buffer
	if err := runUpdate(newTestUpdateCmd(&out), updateOptions{}); err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if !slices.Contains(system.startedNames, agentServiceName) {
		t.Errorf("expected the privileged manager to restart %s, started=%v", agentServiceName, system.startedNames)
	}
	if !slices.Contains(user.startedNames, serveServiceName) {
		t.Errorf("expected the --user manager to restart %s, started=%v", serveServiceName, user.startedNames)
	}
}

func TestRunUpdate_SkipServiceRestartFlag_SkipsRestartCheck(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "v1.1.0"})
	t.Setenv("BOXY_TEST_EXE_PATH", t.TempDir()+"/boxy")

	m := &fakeManager{statusByName: map[string]svcmgr.Status{
		agentServiceName: {Installed: true, Running: true},
	}}
	withFakeSvcManager(t, m)

	var out bytes.Buffer
	err := runUpdate(newTestUpdateCmd(&out), updateOptions{skipServiceRestart: true})
	if err != nil {
		t.Fatalf("runUpdate: %v", err)
	}
	if len(m.statusNames) != 0 {
		t.Errorf("--skip-service-restart must skip the restart check entirely, got Status calls: %v", m.statusNames)
	}
}

func TestRunUpdate_RestartFailure_WarnsButDoesNotFailUpdate(t *testing.T) {
	withVersion(t, "v1.0.0")
	withMockUpdater(t, &mockUpdater{latestVersion: "v1.1.0"})
	t.Setenv("BOXY_TEST_EXE_PATH", t.TempDir()+"/boxy")

	system := &fakeManager{
		statusByName: map[string]svcmgr.Status{
			agentServiceName: {Installed: true, Running: true},
		},
		stopErr: errors.New("access denied"),
	}
	withPerModeFakeSvcManager(t, system, &fakeManager{})

	var out bytes.Buffer
	err := runUpdate(newTestUpdateCmd(&out), updateOptions{})
	if err != nil {
		t.Fatalf("a restart failure must not fail the overall update, got: %v", err)
	}
	if !strings.Contains(out.String(), "warning") || !strings.Contains(out.String(), agentServiceName) {
		t.Errorf("expected a warning mentioning %s, got:\n%s", agentServiceName, out.String())
	}
}

func TestUpdateCommand_SkipServiceRestartFlag_WiresThrough(t *testing.T) {
	withVersion(t, "v1.0.0")

	var capturedOpts updateOptions
	orig := updateNewUpdater
	updateNewUpdater = func(opts updateOptions) updaterIface {
		capturedOpts = opts
		return &mockUpdater{latestVersion: "v1.0.0"}
	}
	t.Cleanup(func() { updateNewUpdater = orig })

	cmd := newUpdateCommand()
	cmd.SetArgs([]string{"--skip-service-restart"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if !capturedOpts.skipServiceRestart {
		t.Error("expected skipServiceRestart=true")
	}
}
