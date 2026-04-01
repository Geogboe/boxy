// Command generate renders install.sh and install.ps1 from templates
// using the shared constants in internal/buildcfg.
//
// Run via: go generate ./scripts/...
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/Geogboe/boxy/internal/buildcfg"
)

// templateData extends buildcfg constants with derived fields needed by
// the templates.
type templateData struct {
	Repo                       string
	BinaryName                 string
	DefaultInstallDir          string
	DefaultInstallDirBackslash string
	APIBase                    string
	DownloadBase               string
}

func newTemplateData() templateData {
	return templateData{
		Repo:                       buildcfg.Repo,
		BinaryName:                 buildcfg.BinaryName,
		DefaultInstallDir:          buildcfg.DefaultInstallDir,
		DefaultInstallDirBackslash: strings.ReplaceAll(buildcfg.DefaultInstallDir, "/", `\`),
		APIBase:                    buildcfg.APIBase,
		DownloadBase:               buildcfg.DownloadBase,
	}
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "generate: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// The generator runs from the package directory containing the
	// //go:generate directive (scripts/).  Templates live in scripts/generate/.
	scriptsDir, err := filepath.Abs(".")
	if err != nil {
		return err
	}
	genDir := filepath.Join(scriptsDir, "generate")

	data := newTemplateData()

	specs := []struct {
		tmpl string
		out  string
	}{
		{"install.sh.tmpl", "install.sh"},
		{"install.ps1.tmpl", "install.ps1"},
	}

	for _, s := range specs {
		tmplPath := filepath.Join(genDir, s.tmpl)
		outPath := filepath.Join(scriptsDir, s.out)

		t, err := template.ParseFiles(tmplPath)
		if err != nil {
			return fmt.Errorf("parse %s: %w", s.tmpl, err)
		}

		var buf bytes.Buffer
		if err := t.Execute(&buf, data); err != nil {
			return fmt.Errorf("execute %s: %w", s.tmpl, err)
		}

		// Normalize to LF so shell scripts work on Unix regardless of
		// the host OS where go generate runs.
		output := bytes.ReplaceAll(buf.Bytes(), []byte("\r\n"), []byte("\n"))

		if err := os.WriteFile(outPath, output, 0o644); err != nil { //nolint:gosec // generated files need to be readable
			return fmt.Errorf("write %s: %w", s.out, err)
		}
		fmt.Printf("generated %s\n", s.out)
	}

	return nil
}
