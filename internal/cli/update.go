package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Geogboe/boxy/internal/buildcfg"
	boxyskills "github.com/Geogboe/boxy/internal/skills"
	"github.com/Geogboe/boxy/internal/svcmgr"
	"github.com/Geogboe/rog/pkg/selfupdate"
	"github.com/spf13/cobra"
)

// updateOptions holds the resolved configuration for a single update invocation.
type updateOptions struct {
	pinnedVersion      string
	proxyURL           string
	token              string
	checkOnly          bool
	prerelease         bool
	skipServiceRestart bool
}

// updaterIface is the narrow interface used by runUpdate, enabling injection in tests.
type updaterIface interface {
	CheckLatest(ctx context.Context) (string, error)
	Install(ctx context.Context, version, exePath string) error
}

// updateNewUpdater is the factory used to create an updaterIface.
// It is a package-level variable so tests can replace it.
var updateNewUpdater = defaultUpdateNewUpdater

func defaultUpdateNewUpdater(opts updateOptions) updaterIface {
	transport := &http.Transport{
		Proxy: updateProxyFunc(opts.proxyURL),
	}
	client := &http.Client{Transport: transport}

	u := &selfupdate.Updater{
		Repo:                    buildcfg.Repo,
		BinaryName:              buildcfg.BinaryName,
		Token:                   opts.token,
		Client:                  client,
		AssetNamer:              buildcfg.AssetName,
		AllowPrereleaseAndDraft: opts.prerelease,
	}
	return &boxyUpdater{u: u, pinnedVersion: opts.pinnedVersion}
}

// boxyUpdater wraps selfupdate.Updater to implement updaterIface.
type boxyUpdater struct {
	u             *selfupdate.Updater
	pinnedVersion string
}

func (b *boxyUpdater) CheckLatest(ctx context.Context) (string, error) {
	if b.pinnedVersion != "" {
		rel, err := b.u.FetchRelease(ctx, b.pinnedVersion)
		if err != nil {
			return "", err
		}
		return rel.Version, nil
	}
	rel, err := b.u.CheckLatest(ctx)
	if err != nil {
		if errors.Is(err, selfupdate.ErrNoStableRelease) {
			return "", fmt.Errorf("%w (re-run with --prerelease to update to a prerelease build)", err)
		}
		return "", err
	}
	return rel.Version, nil
}

func (b *boxyUpdater) Install(ctx context.Context, targetVersion, exePath string) error {
	rel, err := b.u.FetchRelease(ctx, targetVersion)
	if err != nil {
		return err
	}
	return b.u.Install(ctx, rel, exePath)
}

func updateProxyFunc(proxyURL string) func(*http.Request) (*url.URL, error) {
	if proxyURL == "" {
		return http.ProxyFromEnvironment
	}
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return http.ProxyFromEnvironment
	}
	return http.ProxyURL(parsed)
}

func newUpdateCommand() *cobra.Command {
	var (
		checkOnly          bool
		pinnedVersion      string
		proxyURL           string
		prerelease         bool
		skipServiceRestart bool
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update boxy to the latest release",
		Long: `Update the boxy binary in-place to the latest (or a pinned) release from GitHub.

By default, only a stable (non-prerelease, non-draft) release is considered
"latest". Pass --prerelease to update to the newest release regardless of
its prerelease/draft status, e.g. when no stable release has been published yet.

After a successful update, boxy checks whether the default-named boxy-agent
and boxy-serve services (privileged or --user) are installed and currently
running, and restarts each one so it doesn't keep running the pre-update
binary in memory. Named instances (installed with --instance-name) are not
covered — restart those manually. Pass --skip-service-restart to disable
this check entirely.

Environment variables:
  BOXY_GITHUB_TOKEN   GitHub API token to avoid rate limits`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, updateOptions{
				pinnedVersion:      pinnedVersion,
				proxyURL:           proxyURL,
				token:              os.Getenv("BOXY_GITHUB_TOKEN"),
				checkOnly:          checkOnly,
				prerelease:         prerelease,
				skipServiceRestart: skipServiceRestart,
			})
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for updates without installing")
	cmd.Flags().StringVar(&pinnedVersion, "version", "", "Install a specific version (e.g. v0.1.9)")
	cmd.Flags().StringVar(&proxyURL, "proxy", "", "HTTP proxy URL (overrides HTTPS_PROXY env var)")
	cmd.Flags().BoolVar(&prerelease, "prerelease", false, "Consider prerelease/draft releases when checking for the latest version")
	cmd.Flags().BoolVar(&skipServiceRestart, "skip-service-restart", false, "Don't check for or restart an installed boxy-agent/boxy-serve service after updating")

	return cmd
}

func runUpdate(cmd *cobra.Command, opts updateOptions) error {
	ctx := cmd.Context()
	updater := updateNewUpdater(opts)

	current := Version

	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "==> Checking for updates...")
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "    Current version: %s\n", current)

	latest, err := updater.CheckLatest(ctx)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "    Latest version:  %s\n", latest)

	if strings.TrimPrefix(current, "v") == strings.TrimPrefix(latest, "v") {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ Already up to date (%s)\n", latest)
		return nil
	}

	if opts.checkOnly {
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "update available — run 'boxy update' to install")
		return nil
	}

	exePath, err := updateResolveExePath()
	if err != nil {
		return fmt.Errorf("locate current executable: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "==> Downloading boxy %s...\n", latest)
	if err := updater.Install(ctx, latest, exePath); err != nil {
		if updateIsPermissionError(err) {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
				"  %s is not writable by the current user — run as the file's owner\n", exePath)
			return fmt.Errorf("install: permission denied: %w", err)
		}
		return fmt.Errorf("install: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ boxy updated to %s\n", latest)
	if _, err := boxyskills.InstallCanonical(true, latest); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not refresh bundled skills: %v\n", err)
	}
	if !opts.skipServiceRestart {
		restartInstalledDefaultServices(cmd)
	}

	installDir := filepath.Dir(exePath)
	if msg := selfupdate.PathWarningMessage(installDir); msg != "" {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), "\n"+msg)
	}

	return nil
}

// restartInstalledDefaultServices checks whether the default-named
// boxy-agent/boxy-serve services — privileged or --user — are installed
// and currently running, and restarts (stop then start) each one that is,
// so an update doesn't leave a service running the pre-update binary in
// memory. A service that's installed but not currently running is left
// alone: that's a deliberate operator choice, not something update should
// override.
//
// Named instances (installed with --instance-name, see #156) are not
// covered — svcmgr.Manager has no way to enumerate them, only to query a
// name the caller already knows. Restarting those is the operator's job.
//
// Failures here are reported as warnings, not returned as errors: the
// binary update itself already succeeded by the time this runs, and a
// service that can't be restarted (e.g. a permission issue) shouldn't make
// `boxy update` look like it failed.
func restartInstalledDefaultServices(cmd *cobra.Command) {
	targets := []struct {
		name     string
		userMode bool
	}{
		{agentServiceName, false},
		{agentServiceName, true},
		{serveServiceName, false},
		{serveServiceName, true},
	}
	for _, target := range targets {
		mgr, err := svcmgrNewManager(svcmgr.ManagerOptions{UserMode: target.userMode})
		if err != nil {
			continue // this mode isn't available on this platform; nothing to restart
		}
		st, err := mgr.Status(target.name)
		if err != nil || !st.Installed || !st.Running {
			continue
		}
		if err := mgr.Stop(target.name); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not restart %s (stop failed): %v\n", target.name, err)
			continue
		}
		if err := mgr.Start(target.name); err != nil {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: could not restart %s (stopped, but failed to start again — start it manually): %v\n", target.name, err)
			continue
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "✓ restarted %s service\n", target.name)
	}
}

// updateResolveExePath returns the path of the running executable.
// In tests, BOXY_TEST_EXE_PATH can override this.
func updateResolveExePath() (string, error) {
	if p := os.Getenv("BOXY_TEST_EXE_PATH"); p != "" {
		return p, nil
	}
	return os.Executable()
}

func updateIsPermissionError(err error) bool {
	return err != nil && (os.IsPermission(err) ||
		strings.Contains(strings.ToLower(err.Error()), "permission denied") ||
		strings.Contains(strings.ToLower(err.Error()), "access is denied"))
}
