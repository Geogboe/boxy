package skills

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	SkillName        = "boxy-cli"
	VersionFileName  = ".boxy-skill-version"
	SourceFileName   = ".boxy-skill-source"
	assetsRoot       = "assets"
	defaultConfigDir = ".config"
)

//go:embed all:assets
var embeddedAssets embed.FS

func AssetFS() fs.FS {
	return embeddedAssets
}

func CanonicalDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "boxy", "skills"), nil
	}
	return filepath.Join(home, defaultConfigDir, "boxy", "skills"), nil
}

func DefaultAgentDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home: %w", err)
	}
	return filepath.Join(home, ".agents", "skills"), nil
}

func ProjectAgentDir(cwd string) string {
	return filepath.Join(cwd, ".agents", "skills")
}

func CanonicalSkillPath() (string, error) {
	root, err := CanonicalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, SkillName), nil
}

func InstallCanonical(force bool, version string) (string, error) {
	canonicalPath, err := CanonicalSkillPath()
	if err != nil {
		return "", err
	}
	if force {
		if err := os.RemoveAll(canonicalPath); err != nil {
			return "", fmt.Errorf("reset canonical skill dir: %w", err)
		}
	}
	if err := os.MkdirAll(canonicalPath, 0o755); err != nil {
		return "", fmt.Errorf("create canonical skill dir: %w", err)
	}
	if err := copyEmbeddedSkill(canonicalPath); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(canonicalPath, VersionFileName), []byte(strings.TrimSpace(version)+"\n"), 0o644); err != nil {
		return "", fmt.Errorf("write version file: %w", err)
	}
	return canonicalPath, nil
}

func LinkAt(canonicalPath, targetParent string, force bool) (string, bool, error) {
	targetPath := filepath.Join(targetParent, SkillName)
	ready, err := ensureTargetAvailable(targetPath, canonicalPath, force)
	if err != nil {
		return "", false, err
	}
	if ready {
		return targetPath, false, nil
	}
	if err := os.MkdirAll(targetParent, 0o755); err != nil {
		return "", false, fmt.Errorf("create target parent %q: %w", targetParent, err)
	}
	copyFallback, err := createDirLink(canonicalPath, targetPath)
	if err != nil {
		return "", false, fmt.Errorf("link skill into %q: %w", targetParent, err)
	}
	if copyFallback {
		if err := os.WriteFile(filepath.Join(targetPath, SourceFileName), []byte(canonicalPath+"\n"), 0o644); err != nil {
			return "", false, fmt.Errorf("write managed source marker: %w", err)
		}
	}
	return targetPath, copyFallback, nil
}

func RemoveLinkAt(canonicalPath, targetParent string) (bool, error) {
	targetPath := filepath.Join(targetParent, SkillName)
	info, err := os.Lstat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect target %q: %w", targetPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(targetPath)
		if err != nil {
			return false, fmt.Errorf("resolve symlink %q: %w", targetPath, err)
		}
		if !samePath(resolved, canonicalPath) {
			return false, nil
		}
		if err := os.Remove(targetPath); err != nil {
			return false, fmt.Errorf("remove symlink %q: %w", targetPath, err)
		}
		return true, nil
	}
	if !info.IsDir() {
		return false, nil
	}
	markerPath := filepath.Join(targetPath, SourceFileName)
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read managed source marker %q: %w", markerPath, err)
	}
	if !samePath(strings.TrimSpace(string(marker)), canonicalPath) {
		return false, nil
	}
	if err := os.RemoveAll(targetPath); err != nil {
		return false, fmt.Errorf("remove managed copy %q: %w", targetPath, err)
	}
	return true, nil
}

func copyEmbeddedSkill(dst string) error {
	return fs.WalkDir(embeddedAssets, path.Join(assetsRoot, SkillName), func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(name, path.Join(assetsRoot, SkillName)+"/")
		if name == path.Join(assetsRoot, SkillName) {
			rel = "."
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyEmbeddedFile(name, target)
	})
}

func copyEmbeddedFile(srcName, dst string) error {
	src, err := embeddedAssets.Open(srcName)
	if err != nil {
		return fmt.Errorf("open embedded asset %q: %w", srcName, err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create target dir for %q: %w", dst, err)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open target %q: %w", dst, err)
	}
	defer f.Close()
	if _, err := io.Copy(f, src); err != nil {
		return fmt.Errorf("copy embedded asset into %q: %w", dst, err)
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, name)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		return copyLocalFile(name, target)
	})
}

func copyLocalFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

func ensureTargetAvailable(targetPath, canonicalPath string, force bool) (bool, error) {
	info, err := os.Lstat(targetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect target %q: %w", targetPath, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(targetPath)
		if err == nil && samePath(resolved, canonicalPath) {
			return true, nil
		}
	}
	if !force {
		return false, fmt.Errorf("target %q already exists (use --force to replace it)", targetPath)
	}
	if err := os.RemoveAll(targetPath); err != nil {
		return false, fmt.Errorf("remove existing target %q: %w", targetPath, err)
	}
	return false, nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	cleanA := filepath.Clean(a)
	cleanB := filepath.Clean(b)
	return strings.EqualFold(cleanA, cleanB)
}
