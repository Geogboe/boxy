// Command api-doc-gen generates the checked-in REST API reference.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/Geogboe/boxy/internal/server"
)

func main() {
	output := flag.String("out", "docs/api.md", "output Markdown path")
	flag.Parse()
	if err := generate(*output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate(output string) error {
	content := render(server.APIRouteCatalog())
	if err := os.WriteFile(output, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write API docs %q: %w", output, err)
	}
	if err := os.Chmod(output, 0o644); err != nil { //nolint:gosec // generated API documentation is intentionally world-readable
		return fmt.Errorf("set API docs permissions %q: %w", output, err)
	}
	return nil
}

func render(routes []server.APIRoute) string {
	var b strings.Builder
	b.WriteString("# Boxy REST API\n\n")
	b.WriteString("This document is generated from `server.APIRouteCatalog()` by `go generate ./...`. Do not edit it manually.\n\n")
	b.WriteString("## Transport and authentication\n\n")
	b.WriteString("`boxy serve` serves REST over HTTPS by default using the Boxy private CA. Public or enterprise-trusted certificates use the system trust store. Use `--ca-cert <path>` for a Boxy or custom CA. `boxy login --insecure` skips HTTPS verification for explicit development use only; `boxy serve --insecure` disables REST and agent-gRPC TLS.\n\n")
	b.WriteString("All `/api/v1/*` routes except loopback bootstrap require:\n\n")
	b.WriteString("```http\nAuthorization: Bearer <api-key>\n```\n\n")
	b.WriteString("Missing or invalid credentials return `401 Unauthorized`. Valid credentials without the required role return `403 Forbidden`.\n\n")
	b.WriteString("API-key roles:\n\n- `user`: manage owned sandboxes only.\n- `auditor`: read-only access to shared resources.\n- `admin`: full operator access.\n\n")
	b.WriteString("## Routes\n\n")

	lastGroup := ""
	for _, route := range routes {
		if route.Group != lastGroup {
			if lastGroup != "" {
				b.WriteString("\n")
			}
			fmt.Fprintf(&b, "### %s\n\n| Method | Path | Auth | Description |\n|---|---|---|---|\n", route.Group)
			lastGroup = route.Group
		}
		fmt.Fprintf(&b, "| %s | `%s` | %s | %s |\n", route.Method, route.Path, route.Auth, route.Description)
	}

	b.WriteString("\n## Agent list response\n\n")
	b.WriteString("`GET /api/v1/agents` retains the original `id`, `name`, `providers`, and `available` fields and adds the following fields:\n\n")
	b.WriteString("```json\n{\"id\":\"agent-1\",\"name\":\"Lab Hypervisor\",\"providers\":[\"hyperv\"],\"available\":true,\"connected\":true,\"last_seen\":\"2026-08-21T14:30:00Z\",\"availability\":{\"hyperv\":{\"memory_mb\":4096}},\"availability_at\":\"2026-08-21T14:30:00Z\"}\n```\n\n")
	b.WriteString("`connected` indicates an active embedded or remote transport; `available` indicates eligibility for new provisioning and can be false while a remote transport remains connected. `last_seen` and `availability_at` are optional RFC3339 UTC server-receipt timestamps. A missing provider entry in `availability` means no capacity sample was available, not zero capacity. Disconnected remote agents remain listed; the embedded agent has no heartbeat or capacity sample.\n\n")

	b.WriteString("\n## Sandbox create request\n\n")
	b.WriteString("```json\n{\"name\":\"lab\",\"policies\":{\"auto_destroy_after\":\"1h\"},\"requests\":[{\"type\":\"container\",\"profile\":\"web\",\"count\":1}]}\n```\n\n")
	b.WriteString("Sandbox creation returns `202 Accepted` and is fulfilled asynchronously by the daemon. User-key sandboxes are owner-scoped; ownerless legacy sandboxes remain visible only to auditors and admins.\n\n")
	b.WriteString("## Sandbox execution\n\n")
	b.WriteString("`POST /api/v1/sandboxes/{id}/exec` accepts a JSON object with a non-empty `command` array and optional `resource_id` and `timeout` fields. The sandbox must be ready; multi-resource sandboxes require `resource_id`. The default response is bounded JSON. Add `?stream=true` for `application/x-ndjson`: each data event has `type`, `stream`, and base64 `data`; the terminal event has `type=complete`, `exit_code` in `attributes`, and any error.\n\n")
	b.WriteString("Output is bounded (1 MiB total, 64 KiB per chunk) in both modes. In the buffered (default) mode, a command that exceeds the limit returns `413 Payload Too Large` — any output already collected is discarded, since the buffered response has nothing to send until the whole thing is ready. Retry with `stream=true` for output that may be large: it delivers each chunk to the client as soon as it's produced, so exceeding the limit mid-command only truncates the tail — every chunk already sent stays delivered — and surfaces as an `error` field on the terminal `complete` event rather than an HTTP status change (headers are already flushed by the time streaming starts). A context-deadline timeout returns `504 Gateway Timeout`; any other provider/agent failure (including an unsupported-streaming capability error) returns `500 Internal Server Error` in buffered mode, or the same terminal `complete`-event `error` in streaming mode.\n\n")
	b.WriteString("## Diagnostics logs\n\n")
	b.WriteString("`GET /api/v1/diagnostics/logs` is administrator-only. It returns bounded, redacted control-plane and server-observed agent events as `{\"events\": [...], \"next_cursor\": \"...\"}`. Filter with `since`, `level`, `component`, `pool`, `agent`, `resource`, and `limit`; pass the opaque `cursor` returned by a prior response for the next page. Events are retained for up to seven days and 10 MiB, with 100 events per page by default and 1000 as the hard maximum. Credentials, signed URL queries, and secret-bearing fields are removed before events are persisted or rendered. Query access is recorded in a separate audit log.\n\n")
	b.WriteString("## Compatibility\n\n")
	b.WriteString("The API version is part of the URL. Additive response fields are compatible within `/api/v1`; breaking changes require a new API version.\n")
	return b.String()
}
