package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/pterm/pterm"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type loginOptions struct {
	server   string
	apiKey   string
	caCert   string
	insecure bool
}

var loginCredentialsStore = credentials.New

var errLoginPromptCanceled = errors.New("API key prompt canceled")

var loginInteractivePrompt = promptAPIKeyInteractive

var loginIsTerminal = func(file *os.File) bool {
	return term.IsTerminal(int(file.Fd()))
}

var loginClientConfigStore = func(server string) error {
	cfg, err := loadClientConfig()
	if err != nil {
		return err
	}
	cfg.Server = server
	return writeClientConfig(cfg)
}

func newLoginCommand() *cobra.Command {
	var opts loginOptions
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an API key for a Boxy server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if opts.apiKey == "" {
				key, err := promptAPIKey(cmd)
				if err != nil {
					return err
				}
				opts.apiKey = key
			}
			if !cmd.Flags().Changed("insecure") {
				opts.insecure = apiInsecureFromEnvironment()
			}
			if !cmd.Flags().Changed("ca-cert") {
				opts.caCert = os.Getenv("BOXY_CA_CERT")
			}
			return runLogin(cmd.Context(), opts, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	cmd.Flags().StringVar(&opts.server, "server", "", "server URL (overrides BOXY_SERVER and the global client default)")
	cmd.Flags().StringVar(&opts.apiKey, "api-key", "", "API key (prefer interactive prompt to avoid shell history/process-list exposure)")
	cmd.Flags().StringVar(&opts.caCert, "ca-cert", "", "Boxy CA certificate for a self-signed server")
	cmd.Flags().BoolVar(&opts.insecure, "insecure", false, "skip HTTPS certificate verification (development only)")
	return cmd
}

func newLogoutCommand() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove the stored API key for a Boxy server",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runLogout(cmd.Context(), server, cmd.OutOrStdout())
		},
	}
	cmd.Flags().StringVar(&server, "server", "", "server URL (overrides BOXY_SERVER and the global client default)")
	return cmd
}

func promptAPIKey(cmd *cobra.Command) (string, error) {
	in := cmd.InOrStdin()
	if file, ok := in.(*os.File); ok && loginIsTerminal(file) {
		return loginInteractivePrompt(cmd)
	}

	_, _ = fmt.Fprint(cmd.ErrOrStderr(), "API key: ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("read API key: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return "", fmt.Errorf("API key must not be empty")
	}
	return line, nil
}

func promptAPIKeyInteractive(_ *cobra.Command) (string, error) {
	interrupted := false
	value, err := pterm.DefaultInteractiveTextInput.
		WithMask("*").
		WithOnInterruptFunc(func() { interrupted = true }).
		Show("API key (hidden; Ctrl+C to cancel)")
	if interrupted {
		return "", errLoginPromptCanceled
	}
	if err != nil {
		return "", fmt.Errorf("read API key: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("API key must not be empty")
	}
	return value, nil
}

func runLogin(ctx context.Context, opts loginOptions, out, errOut io.Writer) error {
	base := apiBaseURL(opts.server)
	if strings.TrimSpace(opts.apiKey) == "" {
		return fmt.Errorf("API key must not be empty")
	}
	var caPEM []byte
	if opts.caCert != "" {
		data, err := os.ReadFile(opts.caCert)
		if err != nil {
			return fmt.Errorf("read CA certificate: %w", err)
		}
		caPEM = data
	}

	client := apiClientWithMaterial(base, opts.apiKey, caPEM, opts.insecure)
	if _, err := fetchJSON[[]model.Pool](ctx, client, base+"/api/v1/pools"); err != nil {
		return fmt.Errorf("verify API key: %w", err)
	}

	store := loginCredentialsStore()
	if err := store.Set(base, opts.apiKey); err != nil {
		return fmt.Errorf("store API key: %w", err)
	}
	if len(caPEM) != 0 {
		if err := store.SetCA(base, caPEM); err != nil {
			return fmt.Errorf("store CA certificate: %w", err)
		}
	}
	if err := loginClientConfigStore(base); err != nil {
		return fmt.Errorf("store client server default (API key was stored successfully): %w", err)
	}
	_, _ = fmt.Fprintf(out, "Logged in to %s\n", base)
	_ = errOut
	return nil
}

func runLogout(_ context.Context, server string, out io.Writer) error {
	base := apiBaseURL(server)
	if err := loginCredentialsStore().Delete(base); err != nil {
		return fmt.Errorf("remove stored credentials: %w", err)
	}
	_, _ = fmt.Fprintf(out, "Logged out of %s\n", base)
	return nil
}
