package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/Geogboe/boxy/internal/credentials"
	"github.com/Geogboe/boxy/pkg/model"
	"github.com/Geogboe/boxy/pkg/providersdk"
	"github.com/pterm/pterm"
)

type sandboxGuestCredentialResponse struct {
	Credentials []sandboxGuestCredentialDelivery `json:"credentials"`
}

type sandboxGuestCredentialDelivery struct {
	ResourceID string                       `json:"resource_id"`
	Credential *providersdk.GuestCredential `json:"credential"`
}

// guestCredentialStore is injectable so CLI tests can exercise keyring flows
// without touching the host keyring.
var guestCredentialStore = credentials.New

func fetchGuestCredentials(ctx context.Context, client *http.Client, base string, sandboxID model.SandboxID) ([]sandboxGuestCredentialDelivery, error) {
	response, err := fetchJSON[sandboxGuestCredentialResponse](ctx, client, base+"/api/v1/sandboxes/"+string(sandboxID)+"/guest-credential")
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && (apiErr.StatusCode == 404 || apiErr.StatusCode == 410) {
			return nil, nil
		}
		return nil, err
	}
	return response.Credentials, nil
}

func saveGuestCredentials(server string, sandboxID model.SandboxID, deliveries []sandboxGuestCredentialDelivery) error {
	store := guestCredentialStore()
	base := apiBaseURL(server)
	for _, delivery := range deliveries {
		if delivery.Credential == nil || strings.TrimSpace(delivery.ResourceID) == "" {
			return fmt.Errorf("server returned an invalid guest credential delivery")
		}
		if err := store.SetGuestCredential(base, string(sandboxID), delivery.ResourceID, *delivery.Credential); err != nil {
			return fmt.Errorf("save guest credential for resource %s: %w", delivery.ResourceID, err)
		}
	}
	return nil
}

// deleteGuestCredentials removes any keyring entries saved for a sandbox's
// resources. It is best-effort by design: the sandbox is already deleted (or
// accepted for deletion) by the time this runs, and most sandboxes were never
// created with --save-guest-cred in the first place, so a missing or
// inaccessible keyring entry must not fail `sandbox delete` itself. Callers
// should surface returned per-resource errors as warnings, not fatal errors.
func deleteGuestCredentials(server string, sandboxID model.SandboxID, resourceIDs []model.ResourceID) []error {
	store := guestCredentialStore()
	base := apiBaseURL(server)
	var errs []error
	for _, resourceID := range resourceIDs {
		if err := store.DeleteGuestCredential(base, string(sandboxID), string(resourceID)); err != nil {
			errs = append(errs, fmt.Errorf("resource %s: %w", resourceID, err))
		}
	}
	return errs
}

func printGuestCredentials(sbID model.SandboxID, deliveries []sandboxGuestCredentialDelivery) error {
	if len(deliveries) == 0 {
		return nil
	}
	pterm.Println()
	pterm.Bold.Println("  Guest credentials  (shown once — save or use them now)")
	pterm.Println()
	for _, delivery := range deliveries {
		if delivery.Credential == nil {
			return fmt.Errorf("server returned an empty guest credential for resource %s", delivery.ResourceID)
		}
		data, err := json.MarshalIndent(delivery.Credential, "    ", "  ")
		if err != nil {
			return fmt.Errorf("format guest credential for resource %s: %w", delivery.ResourceID, err)
		}
		pterm.Printfln("    sandbox: %s", sbID)
		pterm.Printfln("    resource: %s", delivery.ResourceID)
		pterm.Println(string(data))
	}
	pterm.Println()
	return nil
}

func printSavedGuestCredentials(deliveries []sandboxGuestCredentialDelivery) {
	for _, delivery := range deliveries {
		pterm.Printfln("  Saved guest credential for resource %s in the OS keyring.", delivery.ResourceID)
	}
}

func guestCredentialFromKeyring(ctx context.Context, server, sandboxID, resourceID string) (*providersdk.GuestCredential, error) {
	resourceID = strings.TrimSpace(resourceID)
	if resourceID == "" {
		client := apiClientForServer(server)
		sandbox, err := fetchJSON[model.Sandbox](ctx, client, apiBaseURL(server)+"/api/v1/sandboxes/"+sandboxID)
		if err != nil || len(sandbox.Resources) != 1 {
			// Automatic keyring lookup is best-effort. The server can still
			// execute commands that do not need a guest credential, and a
			// multi-resource sandbox still needs --resource to disambiguate.
			return nil, nil
		}
		resourceID = string(sandbox.Resources[0])
	}
	credential, err := guestCredentialStore().GetGuestCredential(apiBaseURL(server), sandboxID, resourceID)
	if errors.Is(err, credentials.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		// A host keyring may be unavailable in a headless environment. Do
		// not make ordinary exec unusable; the command will proceed without
		// a credential and the server will report if one was required.
		return nil, nil
	}
	return &credential, nil
}
