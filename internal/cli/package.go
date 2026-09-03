package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Geogboe/boxy/pkg/artifact"
	"github.com/Geogboe/boxy/pkg/resourcepack"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newPackageCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "package",
		Short: "Build and inspect resource packages",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newPackageBuildCommand())
	cmd.AddCommand(newPackagePublishCommand())
	cmd.AddCommand(newPackageInspectCommand())
	return cmd
}

func newPackageBuildCommand() *cobra.Command {
	var manifestPath, outputPath string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build an immutable package artifact from a manifest",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPackageBuild(cmd.Context(), manifestPath, outputPath)
		},
	}
	cmd.Flags().StringVar(&manifestPath, "manifest", "", "package manifest path")
	cmd.Flags().StringVar(&outputPath, "output", "", "artifact output path")
	_ = cmd.MarkFlagRequired("manifest")
	_ = cmd.MarkFlagRequired("output")
	return cmd
}

func runPackageBuild(ctx context.Context, manifestPath, outputPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read package manifest: %w", err)
	}
	var manifest resourcepack.Manifest
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&manifest); err != nil {
		return fmt.Errorf("decode package manifest: %w", err)
	}
	isBuiltin := manifest.Builtin != ""
	manifest, err = resourcepack.Compile(manifest)
	if err != nil {
		return err
	}
	manifestData := data
	if isBuiltin {
		manifestData, err = yaml.Marshal(manifest)
		if err != nil {
			return fmt.Errorf("encode compiled package manifest: %w", err)
		}
	}
	value := artifact.Artifact{
		Type:     artifact.ArtifactTypePackage,
		Ref:      artifact.Ref{Type: artifact.ArtifactTypePackage, Name: manifest.Name, Version: manifest.Version},
		Manifest: manifestData,
	}
	if script, _ := manifest.Inputs["script"].(string); script != "" {
		content, err := os.ReadFile(filepath.Join(filepath.Dir(manifestPath), script))
		if err != nil {
			return fmt.Errorf("read package script %q: %w", script, err)
		}
		value.Blobs = map[string][]byte{script: content}
	}
	if err := writePackageArtifact(outputPath, value); err != nil {
		return err
	}
	return nil
}

func newPackagePublishCommand() *cobra.Command {
	var artifactPath, registryPath, configPath, storeName string
	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish a built package artifact to a local or configured registry",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPackagePublishWithOptions(cmd.Context(), artifactPath, registryPath, configPath, storeName)
		},
	}
	cmd.Flags().StringVar(&artifactPath, "artifact", "", "built artifact path")
	cmd.Flags().StringVar(&registryPath, "registry", "", "local artifact registry directory (legacy flow)")
	cmd.Flags().StringVar(&configPath, "config", "", "Boxy config containing the named artifact store")
	cmd.Flags().StringVar(&storeName, "store", "", "configured artifact store name")
	_ = cmd.MarkFlagRequired("artifact")
	return cmd
}

func runPackagePublish(ctx context.Context, artifactPath, registryPath string) error {
	return runPackagePublishWithOptions(ctx, artifactPath, registryPath, "", "")
}

func runPackagePublishWithOptions(ctx context.Context, artifactPath, registryPath, configPath, storeName string) error {
	if strings.TrimSpace(registryPath) != "" && (strings.TrimSpace(configPath) != "" || strings.TrimSpace(storeName) != "") {
		return fmt.Errorf("--registry cannot be combined with --config or --store")
	}
	if strings.TrimSpace(registryPath) == "" {
		if strings.TrimSpace(configPath) == "" || strings.TrimSpace(storeName) == "" {
			return fmt.Errorf("provide --registry, or provide both --config and --store")
		}
	}
	value, err := readPackageArtifact(artifactPath)
	if err != nil {
		return err
	}
	var registry artifact.Registry
	if strings.TrimSpace(registryPath) != "" {
		registry, err = artifact.NewDirectoryRegistry(registryPath)
		if err != nil {
			return err
		}
	} else {
		cfg, _, err := loadConfig(configPath)
		if err != nil {
			return fmt.Errorf("load artifact config: %w", err)
		}
		if err := cfg.Validate(); err != nil {
			return fmt.Errorf("validate artifact config: %w", err)
		}
		registry, err = cfg.ArtifactStore(ctx, storeName)
		if err != nil {
			return err
		}
	}
	if err := registry.Publish(ctx, value); err != nil {
		return fmt.Errorf("publish package: %w", err)
	}
	return nil
}

func newPackageInspectCommand() *cobra.Command {
	var artifactPath string
	cmd := &cobra.Command{
		Use:   "inspect",
		Short: "Inspect a built package artifact",
		RunE: func(cmd *cobra.Command, _ []string) error {
			value, err := readPackageArtifact(artifactPath)
			if err != nil {
				return err
			}
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(value)
		},
	}
	cmd.Flags().StringVar(&artifactPath, "artifact", "", "built artifact path")
	_ = cmd.MarkFlagRequired("artifact")
	return cmd
}

func readPackageArtifact(path string) (artifact.Artifact, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return artifact.Artifact{}, fmt.Errorf("read package artifact: %w", err)
	}
	var value artifact.Artifact
	if err := json.Unmarshal(data, &value); err != nil {
		return artifact.Artifact{}, fmt.Errorf("decode package artifact: %w", err)
	}
	if value.Type != artifact.ArtifactTypePackage {
		return artifact.Artifact{}, fmt.Errorf("artifact type %q is not a package", value.Type)
	}
	if value.Ref.Name == "" || value.Ref.Version == "" {
		return artifact.Artifact{}, fmt.Errorf("package artifact identity is incomplete")
	}
	return value, nil
}

func writePackageArtifact(path string, value artifact.Artifact) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode package artifact: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create package artifact directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write package artifact: %w", err)
	}
	return nil
}
