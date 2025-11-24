package hooks

import (
	"fmt"
	"regexp"
	"strings"
)

// ExpandTemplate expands template variables in a string
// Supported variables:
//
//	${resource.id}    - Resource UUID
//	${resource.ip}    - IP address (if available)
//	${resource.type}  - Resource type (vm, container, process)
//	${provider.id}    - Provider-specific ID
//	${pool.name}      - Pool name
//	${username}       - Allocated username
//	${password}       - Generated password
//	${metadata.key}   - Custom metadata value
func ExpandTemplate(template string, ctx HookContext) string {
	result := template

	// Simple variable replacements
	replacements := map[string]string{
		"${resource.id}":   ctx.ResourceID,
		"${resource.ip}":   ctx.ResourceIP,
		"${resource.type}": ctx.ResourceType,
		"${provider.id}":   ctx.ProviderID,
		"${pool.name}":     ctx.PoolName,
		"${username}":      ctx.Username,
		"${password}":      ctx.Password,
	}

	for key, value := range replacements {
		result = strings.ReplaceAll(result, key, value)
	}

	// Handle metadata variables: ${metadata.key}
	metadataRegex := regexp.MustCompile(`\$\{metadata\.([a-zA-Z0-9_-]+)\}`)
	result = metadataRegex.ReplaceAllStringFunc(result, func(match string) string {
		// Extract key from ${metadata.key}
		matches := metadataRegex.FindStringSubmatch(match)
		if len(matches) > 1 {
			key := matches[1]
			if value, ok := ctx.Metadata[key]; ok {
				return value
			}
		}
		// Keep original if not found
		return match
	})

	return result
}

// GetShellCommand returns the shell command to execute a script
func GetShellCommand(shell ShellType, script string) ([]string, error) {
	switch shell {
	case ShellBash:
		return []string{"bash", "-c", script}, nil
	case ShellPowerShell:
		// Use PowerShell Core if available, fallback to Windows PowerShell
		// -Command: Execute command and exit
		// -NoProfile: Don't load profile scripts (faster startup)
		return []string{"pwsh", "-NoProfile", "-Command", script}, nil
	case ShellPython:
		return []string{"python3", "-c", script}, nil
	default:
		return nil, fmt.Errorf("unsupported shell type: %s", shell)
	}
}

// ValidateHook validates a hook configuration
func ValidateHook(hook Hook) error {
	if hook.Name == "" {
		return fmt.Errorf("hook name is required")
	}

	if hook.Type != HookTypeScript {
		return fmt.Errorf("unsupported hook type: %s", hook.Type)
	}

	if hook.Shell == "" {
		return fmt.Errorf("hook %s: shell is required", hook.Name)
	}

	if hook.Shell != ShellBash && hook.Shell != ShellPowerShell && hook.Shell != ShellPython {
		return fmt.Errorf("hook %s: unsupported shell: %s", hook.Name, hook.Shell)
	}

	if hook.Inline == "" && hook.Path == "" {
		return fmt.Errorf("hook %s: either 'inline' or 'path' must be specified", hook.Name)
	}

	if hook.Inline != "" && hook.Path != "" {
		return fmt.Errorf("hook %s: cannot specify both 'inline' and 'path'", hook.Name)
	}

	if hook.Retry < 0 {
		return fmt.Errorf("hook %s: retry count cannot be negative", hook.Name)
	}

	return nil
}
