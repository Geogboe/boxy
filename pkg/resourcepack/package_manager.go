package resourcepack

import (
	"fmt"
	"sort"
	"strings"
)

// BuiltinPackageManager identifies the declarative package-manager recipe.
const BuiltinPackageManager = "package-manager"

// Compile validates and normalizes a package manifest. Built-in recipes are
// compiled into the existing immutable inline-script package format. Regular
// manifests are validated and returned unchanged.
func Compile(manifest Manifest) (Manifest, error) {
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	if manifest.Builtin == "" {
		return manifest, nil
	}

	manager, packages, err := packageManagerParameters(manifest.Inputs)
	if err != nil {
		return Manifest{}, fmt.Errorf("%w: %w", ErrInvalidManifest, err)
	}
	method, inline, err := packageManagerScript(manager, packages)
	if err != nil {
		return Manifest{}, err
	}
	manifest.Builtin = ""
	manifest.Method = method
	manifest.Inputs = map[string]any{"inline": inline}
	return manifest, nil
}

func validatePackageManagerInputs(inputs map[string]any) error {
	_, _, err := packageManagerParameters(inputs)
	return err
}

func packageManagerParameters(inputs map[string]any) (string, []string, error) {
	if len(inputs) == 0 {
		return "", nil, fmt.Errorf("package-manager inputs.parameters is required")
	}
	for key := range inputs {
		if key != "parameters" {
			return "", nil, fmt.Errorf("unsupported package-manager input %q", key)
		}
	}
	rawParameters, ok := inputs["parameters"]
	if !ok {
		return "", nil, fmt.Errorf("package-manager inputs.parameters is required")
	}
	parameters, ok := stringAnyMap(rawParameters)
	if !ok {
		return "", nil, fmt.Errorf("package-manager inputs.parameters must be an object")
	}
	for key := range parameters {
		if key != "manager" && key != "packages" {
			return "", nil, fmt.Errorf("unsupported package-manager option %q", key)
		}
	}
	rawManager, ok := parameters["manager"]
	if !ok {
		return "", nil, fmt.Errorf("package-manager option manager is required")
	}
	managerValue, ok := rawManager.(string)
	if !ok || strings.TrimSpace(managerValue) == "" {
		return "", nil, fmt.Errorf("package-manager option manager must be a non-empty string")
	}
	manager := strings.ToLower(strings.TrimSpace(managerValue))
	if !supportedPackageManagers[manager].supported() {
		return "", nil, fmt.Errorf("unsupported package manager %q", managerValue)
	}

	rawPackages, ok := parameters["packages"]
	if !ok {
		return "", nil, fmt.Errorf("package-manager option packages is required")
	}
	packages, err := packageManagerPackageIDs(rawPackages)
	if err != nil {
		return "", nil, err
	}
	return manager, packages, nil
}

func packageManagerPackageIDs(raw any) ([]string, error) {
	var values []any
	switch packages := raw.(type) {
	case []any:
		values = packages
	case []string:
		values = make([]any, len(packages))
		for i, packageID := range packages {
			values[i] = packageID
		}
	default:
		return nil, fmt.Errorf("package-manager option packages must be an array of strings")
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("package-manager option packages must not be empty")
	}

	packages := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for i, rawPackage := range values {
		packageID, ok := rawPackage.(string)
		if !ok {
			return nil, fmt.Errorf("package-manager option packages[%d] must be a string", i)
		}
		if packageID == "" || strings.TrimSpace(packageID) != packageID {
			return nil, fmt.Errorf("package-manager option packages[%d] must be a non-empty identifier", i)
		}
		if !isSafePackageID(packageID) {
			return nil, fmt.Errorf("package-manager option packages[%d] contains unsafe characters", i)
		}
		key := strings.ToLower(packageID)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("package-manager option packages contains duplicate %q", packageID)
		}
		seen[key] = struct{}{}
		packages = append(packages, packageID)
	}
	sort.Strings(packages)
	return packages, nil
}

func isSafePackageID(value string) bool {
	if value == "" || !isASCIILetterOrDigit(rune(value[0])) {
		return false
	}
	for _, r := range value {
		if isASCIILetterOrDigit(r) {
			continue
		}
		switch r {
		case '.', '_', '+', '-', '@', ':', '/', '=':
		default:
			return false
		}
	}
	return true
}

func isASCIILetterOrDigit(value rune) bool {
	return (value >= 'A' && value <= 'Z') || (value >= 'a' && value <= 'z') || (value >= '0' && value <= '9')
}

func stringAnyMap(value any) (map[string]any, bool) {
	if values, ok := value.(map[string]any); ok {
		return values, true
	}
	values, ok := value.(map[string]string)
	if !ok {
		return nil, false
	}
	converted := make(map[string]any, len(values))
	for key, item := range values {
		converted[key] = item
	}
	return converted, true
}

type packageManagerRecipe struct {
	executable string
	method     Method
}

func (r packageManagerRecipe) supported() bool {
	return r.executable != ""
}

var supportedPackageManagers = map[string]packageManagerRecipe{
	"apt":        {executable: "apt-get", method: MethodShell},
	"apk":        {executable: "apk", method: MethodShell},
	"winget":     {executable: "winget", method: MethodPowerShell},
	"chocolatey": {executable: "choco", method: MethodPowerShell},
}

func packageManagerScript(manager string, packages []string) (Method, string, error) {
	recipe, ok := supportedPackageManagers[manager]
	if !ok {
		return "", "", fmt.Errorf("%w: unsupported package manager %q", ErrInvalidManifest, manager)
	}
	if recipe.method == MethodShell {
		return recipe.method, shellPackageManagerScript(manager, recipe.executable, packages), nil
	}
	return recipe.method, powershellPackageManagerScript(manager, recipe.executable, packages), nil
}

func shellPackageManagerScript(manager, executable string, packages []string) string {
	var b strings.Builder
	b.WriteString("set -eu\n")
	fmt.Fprintf(&b, "command -v %s >/dev/null 2>&1 || { echo %q >&2; exit 127; }\n", executable, "required package manager "+manager+" ("+executable+") is not installed")
	switch manager {
	case "apt":
		b.WriteString("DEBIAN_FRONTEND=noninteractive apt-get update\n")
		b.WriteString("DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends ")
	case "apk":
		b.WriteString("apk add --no-cache ")
	}
	b.WriteString(strings.Join(packages, " "))
	b.WriteByte('\n')
	return b.String()
}

func powershellPackageManagerScript(manager, executable string, packages []string) string {
	var b strings.Builder
	b.WriteString("$ErrorActionPreference = 'Stop'\n")
	fmt.Fprintf(&b, "if (-not (Get-Command -Name '%s' -ErrorAction SilentlyContinue)) { throw 'required package manager %s (%s) is not installed' }\n", executable, manager, executable)
	b.WriteString("$packages = @(\n")
	for _, packageID := range packages {
		fmt.Fprintf(&b, "    '%s'\n", packageID)
	}
	b.WriteString(")\n")
	b.WriteString("foreach ($package in $packages) {\n")
	if manager == "winget" {
		b.WriteString("    & winget install --id $package --exact --silent --accept-source-agreements --accept-package-agreements\n")
	} else {
		b.WriteString("    & choco install $package --yes --no-progress\n")
	}
	fmt.Fprintf(&b, "    if ($LASTEXITCODE -ne 0) { throw 'package manager %s failed for ' + $package + ' with exit code ' + $LASTEXITCODE }\n", manager)
	b.WriteString("}\n")
	return b.String()
}
