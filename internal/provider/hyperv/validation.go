package hyperv

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// Validation functions to prevent PowerShell injection and ensure operational safety

// validateVMName validates VM names to prevent injection attacks
func validateVMName(name string) error {
	if name == "" {
		return fmt.Errorf("VM name cannot be empty")
	}

	if len(name) > 100 {
		return fmt.Errorf("VM name too long (max 100 characters)")
	}

	// Only allow alphanumeric, hyphens, underscores
	// This prevents PowerShell injection via semicolons, quotes, backticks, etc.
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9-_]+$`, name)
	if !matched {
		return fmt.Errorf("VM name contains invalid characters (allowed: a-z, A-Z, 0-9, -, _)")
	}

	// Prevent Windows reserved names
	reserved := []string{"CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4",
		"COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4",
		"LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}
	upperName := strings.ToUpper(name)
	for _, r := range reserved {
		if upperName == r {
			return fmt.Errorf("VM name '%s' is a reserved Windows name", name)
		}
	}

	return nil
}

// validateSnapshotName validates snapshot names
func validateSnapshotName(name string) error {
	if name == "" {
		return fmt.Errorf("snapshot name cannot be empty")
	}

	if len(name) > 100 {
		return fmt.Errorf("snapshot name too long (max 100 characters)")
	}

	// Check for dangerous characters first (injection prevention)
	dangerous := []string{";", "`", "$", "|", "&", "<", ">", "^", `"`, `'`}
	for _, char := range dangerous {
		if strings.Contains(name, char) {
			return fmt.Errorf("snapshot name contains dangerous character '%s'", char)
		}
	}

	// Allow alphanumeric, spaces, hyphens, underscores
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9 -_]+$`, name)
	if !matched {
		return fmt.Errorf("snapshot name contains invalid characters (allowed: a-z, A-Z, 0-9, space, -, _)")
	}

	return nil
}

// validatePath validates file paths to prevent injection
func validatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path cannot be empty")
	}

	// Check for dangerous characters that could enable injection
	// Note: backslash is allowed (Windows paths), but other dangerous chars are not
	dangerous := []string{";", "`", "$", "|", "&", "<", ">", "^"}
	for _, char := range dangerous {
		if strings.Contains(path, char) {
			return fmt.Errorf("path contains dangerous character '%s'", char)
		}
	}

	// Ensure path is absolute (no relative path traversal)
	// Handle both Unix paths and Windows paths (C:\, D:\, etc.)
	if !filepath.IsAbs(path) {
		// Also check for Windows absolute paths (drive letter + colon + backslash)
		matched, _ := regexp.MatchString(`^[A-Za-z]:\\`, path)
		if !matched {
			return fmt.Errorf("path must be absolute")
		}
	}

	return nil
}

// validateSwitchName validates virtual switch names
func validateSwitchName(name string) error {
	if name == "" {
		return fmt.Errorf("switch name cannot be empty")
	}

	if len(name) > 100 {
		return fmt.Errorf("switch name too long (max 100 characters)")
	}

	// Check for dangerous characters first (injection prevention)
	dangerous := []string{";", "`", "$", "|", "&", "<", ">", "^", `"`, `'`}
	for _, char := range dangerous {
		if strings.Contains(name, char) {
			return fmt.Errorf("switch name contains dangerous character '%s'", char)
		}
	}

	// Allow alphanumeric, spaces, hyphens, underscores
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9 -_]+$`, name)
	if !matched {
		return fmt.Errorf("switch name contains invalid characters")
	}

	return nil
}

// validateResourceLimits validates CPU and memory values
func validateResourceLimits(cpus int, memoryMB int) error {
	// CPU validation
	if cpus < 1 {
		return fmt.Errorf("CPU count must be at least 1")
	}
	if cpus > 64 {
		return fmt.Errorf("CPU count too high (max 64)")
	}

	// Memory validation
	if memoryMB < 512 {
		return fmt.Errorf("memory must be at least 512 MB")
	}
	if memoryMB > 1048576 { // 1TB
		return fmt.Errorf("memory too high (max 1TB)")
	}

	return nil
}

// escapePowerShellString escapes a string for safe use in PowerShell
// Uses single quotes which prevent variable expansion
func escapePowerShellString(s string) string {
	// In PowerShell single-quoted strings, only single quotes need escaping (doubled)
	return strings.ReplaceAll(s, "'", "''")
}

// validateSnapshotOperation validates snapshot operation types
func validateSnapshotOperation(op string) error {
	valid := map[string]bool{
		"create":  true,
		"restore": true,
		"delete":  true,
	}

	if !valid[op] {
		return fmt.Errorf("invalid snapshot operation '%s' (valid: create, restore, delete)", op)
	}

	return nil
}

// validateImageName validates base image names
func validateImageName(name string) error {
	if name == "" {
		return fmt.Errorf("image name cannot be empty")
	}

	if len(name) > 100 {
		return fmt.Errorf("image name too long (max 100 characters)")
	}

	// Allow alphanumeric, hyphens, underscores, dots (for versions)
	matched, _ := regexp.MatchString(`^[a-zA-Z0-9-_.]+$`, name)
	if !matched {
		return fmt.Errorf("image name contains invalid characters (allowed: a-z, A-Z, 0-9, -, _, .)")
	}

	// Prevent path traversal
	if strings.Contains(name, "..") {
		return fmt.Errorf("image name cannot contain '..'")
	}

	return nil
}
