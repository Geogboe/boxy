package cli

import (
	"fmt"
	"time"

	"github.com/Geogboe/boxy/pkg/model"
	"github.com/spf13/cobra"
)

func newAdminCommand() *cobra.Command {
	var server string
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Manage Boxy operator access and shared daemon state",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.PersistentFlags().StringVar(&server, "server", "", "server URL (overrides BOXY_SERVER and the global client default)")
	cmd.PersistentFlags().String("ca-cert", "", "Boxy CA certificate for a self-signed server")
	cmd.PersistentFlags().Bool("insecure", false, "skip HTTPS certificate verification (development only)")
	cmd.AddCommand(newAPIKeyAdminCommand(func() string { return server }))
	return cmd
}

type createAPIKeyRequestCLI struct {
	Name    string           `json:"name,omitempty"`
	Role    model.APIKeyRole `json:"role"`
	Expires string           `json:"expires,omitempty"`
}

type apiKeyResponse struct {
	ID        string           `json:"id"`
	Key       string           `json:"key"`
	Name      string           `json:"name,omitempty"`
	Role      model.APIKeyRole `json:"role"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
}

type apiKeySummaryResponse struct {
	ID        string           `json:"id"`
	Name      string           `json:"name,omitempty"`
	Role      model.APIKeyRole `json:"role"`
	CreatedAt time.Time        `json:"created_at"`
	ExpiresAt *time.Time       `json:"expires_at,omitempty"`
	RevokedAt *time.Time       `json:"revoked_at,omitempty"`
}

func newAPIKeyAdminCommand(serverAddr func() string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api-key",
		Short: "Create, list, revoke, and bootstrap operator API keys",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newAPIKeyBootstrapCommand(serverAddr))
	cmd.AddCommand(newAPIKeyCreateCommand(serverAddr))
	cmd.AddCommand(newAPIKeyListCommand(serverAddr))
	cmd.AddCommand(newAPIKeyRevokeCommand(serverAddr))
	return cmd
}

func newAPIKeyBootstrapCommand(serverAddr func() string) *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Create the first local administrator API key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			base := apiBaseURL(serverAddr())
			client, err := apiClientForCommand(cmd, serverAddr())
			if err != nil {
				return err
			}
			resp, err := postJSON[map[string]any, apiKeyResponse](cmd.Context(), client, base+"/api/v1/api-keys/bootstrap", map[string]any{"name": name})
			if err != nil {
				return err
			}
			printAPIKeyCreated(cmd, resp, "bootstrap")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "optional name for the bootstrap key")
	return cmd
}

func newAPIKeyCreateCommand(serverAddr func() string) *cobra.Command {
	var name string
	var role model.APIKeyRole
	var expires string
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an operator API key",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			base := apiBaseURL(serverAddr())
			client, err := apiClientForCommand(cmd, serverAddr())
			if err != nil {
				return err
			}
			request := createAPIKeyRequestCLI{Name: name, Role: role, Expires: expires}
			resp, err := postJSON[createAPIKeyRequestCLI, apiKeyResponse](cmd.Context(), client, base+"/api/v1/api-keys", request)
			if err != nil {
				return err
			}
			printAPIKeyCreated(cmd, resp, "created")
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "optional key name")
	cmd.Flags().Var((*apiKeyRoleValue)(&role), "role", "key role: user, auditor, or admin")
	cmd.Flags().StringVar(&expires, "expires", "", "optional expiry as a positive Go duration (e.g. 24h)")
	return cmd
}

type apiKeyRoleValue model.APIKeyRole

func (v *apiKeyRoleValue) String() string { return string(*v) }
func (v *apiKeyRoleValue) Set(value string) error {
	role := model.APIKeyRole(value)
	if !role.Valid() {
		return fmt.Errorf("role must be one of user, auditor, or admin")
	}
	*v = apiKeyRoleValue(role)
	return nil
}
func (v *apiKeyRoleValue) Type() string { return "role" }

func newAPIKeyListCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List operator API keys",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			base := apiBaseURL(serverAddr())
			client, err := apiClientForCommand(cmd, serverAddr())
			if err != nil {
				return err
			}
			keys, err := fetchJSON[[]apiKeySummaryResponse](cmd.Context(), client, base+"/api/v1/api-keys")
			if err != nil {
				return err
			}
			for _, key := range keys {
				expires := "never"
				if key.ExpiresAt != nil {
					expires = key.ExpiresAt.Format(time.RFC3339)
				}
				status := "active"
				if key.RevokedAt != nil {
					status = "revoked"
				}
				_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s\t%s\n", key.ID, key.Name, key.Role, status, expires)
			}
			return nil
		},
	}
}

func newAPIKeyRevokeCommand(serverAddr func() string) *cobra.Command {
	return &cobra.Command{
		Use:   "revoke <id>",
		Short: "Revoke an operator API key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := validatePathID("API key id", args[0])
			if err != nil {
				return err
			}
			base := apiBaseURL(serverAddr())
			client, err := apiClientForCommand(cmd, serverAddr())
			if err != nil {
				return err
			}
			if err := deleteNoContent(cmd.Context(), client, base+"/api/v1/api-keys/"+id); err != nil {
				return err
			}
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "revoked API key %s\n", args[0])
			return nil
		},
	}
}

func printAPIKeyCreated(cmd *cobra.Command, response apiKeyResponse, verb string) {
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s API key\n", verb)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  key: %s\n", response.Key)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  id: %s\n", response.ID)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  role: %s\n", response.Role)
	if response.Name != "" {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  name: %s\n", response.Name)
	}
	if response.ExpiresAt != nil {
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "  expires: %s\n", response.ExpiresAt.Format(time.RFC3339))
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "  Store this key now; it will not be shown again.")
}
