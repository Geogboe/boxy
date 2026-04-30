package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	boxyskills "github.com/Geogboe/boxy/internal/skills"
	"github.com/spf13/cobra"
)

var (
	skillsInstallCanonical = boxyskills.InstallCanonical
	skillsLinkAt           = boxyskills.LinkAt
	skillsRemoveLinkAt     = boxyskills.RemoveLinkAt
	skillsDefaultAgentDir  = boxyskills.DefaultAgentDir
	skillsProjectAgentDir  = boxyskills.ProjectAgentDir
)

type skillsTargetSelection struct {
	user    bool
	project bool
	paths   []string
}

func newSkillsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skills",
		Short: "Install and manage bundled agent skills",
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	cmd.AddCommand(newSkillsInstallCommand())
	cmd.AddCommand(newSkillsUninstallCommand())
	return cmd
}

func newSkillsInstallCommand() *cobra.Command {
	var sel skillsTargetSelection
	var force bool

	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install the bundled Boxy agent skill",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsInstall(cmd, sel, force)
		},
	}

	cmd.Flags().BoolVar(&sel.user, "user", false, "link the skill into ~/.agents/skills")
	cmd.Flags().BoolVar(&sel.project, "project", false, "link the skill into ./.agents/skills in the current working directory")
	cmd.Flags().StringArrayVar(&sel.paths, "path", nil, "additional directory to receive a boxy-cli link or managed copy")
	cmd.Flags().BoolVar(&force, "force", false, "replace existing managed targets and refresh the canonical skill copy")
	return cmd
}

func newSkillsUninstallCommand() *cobra.Command {
	var sel skillsTargetSelection
	var purge bool

	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove installed Boxy agent skill links or copies",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillsUninstall(cmd, sel, purge)
		},
	}

	cmd.Flags().BoolVar(&sel.user, "user", false, "remove the skill from ~/.agents/skills")
	cmd.Flags().BoolVar(&sel.project, "project", false, "remove the skill from ./.agents/skills in the current working directory")
	cmd.Flags().StringArrayVar(&sel.paths, "path", nil, "additional directory to remove boxy-cli from")
	cmd.Flags().BoolVar(&purge, "purge", false, "also delete the canonical ~/.config/boxy/skills/boxy-cli copy")
	return cmd
}

func runSkillsInstall(cmd *cobra.Command, sel skillsTargetSelection, force bool) error {
	canonicalPath, err := skillsInstallCanonical(force, Version)
	if err != nil {
		return err
	}

	targets, err := resolveSkillTargetParents(sel)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Canonical: %s\n", canonicalPath)
	for _, targetParent := range targets {
		targetPath, copyFallback, err := skillsLinkAt(canonicalPath, targetParent, force)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Linked: %s\n", targetPath)
		if copyFallback {
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warning: created a managed copy at %s because symlinks are not permitted on this system\n", targetPath)
		}
	}
	_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Use --path to add agent-specific locations such as .claude/skills or .github/skills.")
	return nil
}

func runSkillsUninstall(cmd *cobra.Command, sel skillsTargetSelection, purge bool) error {
	canonicalPath, err := boxyskills.CanonicalSkillPath()
	if err != nil {
		return err
	}
	targets, err := resolveSkillTargetParents(sel)
	if err != nil {
		return err
	}
	for _, targetParent := range targets {
		removed, err := skillsRemoveLinkAt(canonicalPath, targetParent)
		if err != nil {
			return err
		}
		if removed {
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Removed: %s\n", filepath.Join(targetParent, boxyskills.SkillName))
		}
	}
	if purge {
		if err := os.RemoveAll(canonicalPath); err != nil {
			return fmt.Errorf("purge canonical skill %q: %w", canonicalPath, err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Purged: %s\n", canonicalPath)
	}
	return nil
}

func resolveSkillTargetParents(sel skillsTargetSelection) ([]string, error) {
	wantsExplicit := sel.user || sel.project || len(sel.paths) > 0
	var targets []string
	if !wantsExplicit || sel.user {
		userDir, err := skillsDefaultAgentDir()
		if err != nil {
			return nil, err
		}
		targets = append(targets, userDir)
	}
	if sel.project {
		wd, err := effectiveWD()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory: %w", err)
		}
		targets = append(targets, skillsProjectAgentDir(wd))
	}
	for _, p := range sel.paths {
		if p == "" {
			continue
		}
		targets = append(targets, resolveRelative(p))
	}
	return uniqueSortedPaths(targets), nil
}

func uniqueSortedPaths(paths []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range paths {
		clean := filepath.Clean(p)
		key := strings.ToLower(clean)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}

func newHelpCommand(root *cobra.Command) *cobra.Command {
	helpCmd := &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return root.Help()
			}
			target, _, err := root.Find(args)
			if err != nil {
				return err
			}
			if target == nil {
				return fmt.Errorf("unknown help topic %q", strings.Join(args, " "))
			}
			return target.Help()
		},
	}
	helpCmd.AddCommand(newHelpAllCommand(root))
	return helpCmd
}

func newHelpAllCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:   "all",
		Short: "Print help for every command",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHelpAll(cmd.OutOrStdout(), root)
		},
	}
}

func runHelpAll(w io.Writer, root *cobra.Command) error {
	var commands []*cobra.Command
	collectCommands(root, &commands)
	for idx, command := range commands {
		if idx > 0 {
			if _, err := fmt.Fprintln(w, "\n---"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "# %s\n\n", command.CommandPath()); err != nil {
			return err
		}
		var buf bytes.Buffer
		command.SetOut(&buf)
		command.SetErr(&buf)
		if err := command.Help(); err != nil {
			return err
		}
		if _, err := io.Copy(w, &buf); err != nil {
			return err
		}
	}
	return nil
}

func collectCommands(command *cobra.Command, out *[]*cobra.Command) {
	if command.Hidden {
		return
	}
	*out = append(*out, command)
	for _, child := range command.Commands() {
		collectCommands(child, out)
	}
}
