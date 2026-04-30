package skills_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/Geogboe/boxy/internal/cli"
	boxyskills "github.com/Geogboe/boxy/internal/skills"
	"github.com/spf13/cobra"
)

func TestBundledSkillMentionsAllCommands(t *testing.T) {
	t.Parallel()

	content := readAllSkillText(t)
	root := cli.NewRootCommand()
	for _, token := range commandTokens(root) {
		if !strings.Contains(content, token) {
			t.Fatalf("bundled skill content missing command token %q", token)
		}
	}
}

func readAllSkillText(t *testing.T) string {
	t.Helper()
	var builder strings.Builder
	err := fs.WalkDir(boxyskills.AssetFS(), "assets/boxy-cli", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}
		data, err := fs.ReadFile(boxyskills.AssetFS(), path)
		if err != nil {
			return err
		}
		builder.WriteByte('\n')
		builder.WriteString(bytesToLower(data))
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
	return builder.String()
}

func commandTokens(root *cobra.Command) []string {
	seen := map[string]struct{}{}
	var tokens []string
	var walk func(*cobra.Command)
	walk = func(cmd *cobra.Command) {
		if cmd.Hidden {
			return
		}
		parts := strings.Fields(strings.ToLower(cmd.Use))
		if len(parts) > 0 {
			token := parts[0]
			if !strings.HasPrefix(token, "[") && !strings.HasPrefix(token, "<") {
				if _, ok := seen[token]; !ok {
					seen[token] = struct{}{}
					tokens = append(tokens, token)
				}
			}
		}
		for _, child := range cmd.Commands() {
			walk(child)
		}
	}
	walk(root)
	return tokens
}

func bytesToLower(data []byte) string {
	return strings.ToLower(string(data))
}
