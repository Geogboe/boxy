package cli

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Geogboe/boxy/internal/buildcfg"
	"github.com/Geogboe/rog/pkg/selfupdate"
	"github.com/spf13/cobra"
)

// updateOptions holds the resolved configuration for a single update invocation.
type updateOptions struct {
	pinnedVersion string
	proxyURL      string
	token         string
	checkOnly     bool
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
		Repo:       buildcfg.Repo,
		BinaryName: buildcfg.BinaryName,
		Token:      opts.token,
		Client:     client,
		AssetNamer: buildcfg.AssetName,
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
		checkOnly     bool
		pinnedVersion string
		proxyURL      string
	)

	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update boxy to the latest release",
		Long: `Update the boxy binary in-place to the latest (or a pinned) release from GitHub.

Environment variables:
  BOXY_GITHUB_TOKEN   GitHub API token to avoid rate limits`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd, updateOptions{
				pinnedVersion: pinnedVersion,
				proxyURL:      proxyURL,
				token:         os.Getenv("BOXY_GITHUB_TOKEN"),
				checkOnly:     checkOnly,
			})
		},
	}

	cmd.Flags().BoolVar(&checkOnly, "check", false, "Check for updates without installing")
	cmd.Flags().StringVar(&pinnedVersion, "version", "", "Install a specific version (e.g. v0.1.9)")
	cmd.Flags().StringVar(&proxyURL, "proxy", "", "HTTP proxy URL (overrides HTTPS_PROXY env var)")

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

	installDir := filepath.Dir(exePath)
	if msg := selfupdate.PathWarningMessage(installDir); msg != "" {
		_, _ = fmt.Fprint(cmd.OutOrStdout(), "\n"+msg)
	}

	return nil
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
