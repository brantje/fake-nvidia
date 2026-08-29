package injection

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/brantje/fake-nvidia/internal/runtimebundle"
)

const (
	markerName    = ".fake-nvidia-injection"
	markerContent = "fake-nvidia injection root v1\n"
)

// Layout describes a prepared fake-nvidia container injection root.
type Layout struct {
	Root          string
	RuntimeDir    string
	StateDir      string
	ConfigPath    string
	OverridesPath string
}

// Prepare copies an existing runtime bundle into an isolated injection root and
// writes the rendered Mock NVML configuration used by consumer containers.
// Existing paths are replaced only when they were previously created by this
// package, preventing accidental deletion of unrelated host files.
func Prepare(root, runtimeRoot string, configYAML []byte) (Layout, error) {
	if len(bytes.TrimSpace(configYAML)) == 0 {
		return Layout{}, errors.New("rendered Mock NVML config is required")
	}

	rootAbs, err := cleanAbsolute(root)
	if err != nil {
		return Layout{}, fmt.Errorf("injection root: %w", err)
	}
	runtimeAbs, err := cleanAbsolute(runtimeRoot)
	if err != nil {
		return Layout{}, fmt.Errorf("runtime root: %w", err)
	}
	if err := validateSeparation(rootAbs, runtimeAbs); err != nil {
		return Layout{}, err
	}
	if err := runtimebundle.New(runtimeAbs).Validate(); err != nil {
		return Layout{}, err
	}
	if err := requireOwnedOrAbsent(rootAbs); err != nil {
		return Layout{}, err
	}

	parent := filepath.Dir(rootAbs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return Layout{}, fmt.Errorf("create injection parent: %w", err)
	}
	tmp, err := os.MkdirTemp(parent, ".fake-nvidia-prepare-")
	if err != nil {
		return Layout{}, fmt.Errorf("create temporary injection root: %w", err)
	}
	keepTmp := false
	defer func() {
		if !keepTmp {
			_ = os.RemoveAll(tmp)
		}
	}()

	layout := layoutFor(tmp)
	if err := copyTree(runtimeAbs, layout.RuntimeDir); err != nil {
		return Layout{}, fmt.Errorf("copy runtime bundle: %w", err)
	}
	if err := os.MkdirAll(layout.StateDir, 0o755); err != nil {
		return Layout{}, fmt.Errorf("create state directory: %w", err)
	}
	if err := os.WriteFile(layout.ConfigPath, configYAML, 0o644); err != nil {
		return Layout{}, fmt.Errorf("write Mock NVML config: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, markerName), []byte(markerContent), 0o644); err != nil {
		return Layout{}, fmt.Errorf("write injection marker: %w", err)
	}

	if _, err := os.Lstat(rootAbs); err == nil {
		if err := os.RemoveAll(rootAbs); err != nil {
			return Layout{}, fmt.Errorf("replace existing injection root: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Layout{}, fmt.Errorf("inspect existing injection root: %w", err)
	}
	if err := os.Rename(tmp, rootAbs); err != nil {
		return Layout{}, fmt.Errorf("install injection root: %w", err)
	}
	keepTmp = true
	return layoutFor(rootAbs), nil
}

// Down removes a prepared injection root. It refuses to delete directories not
// marked as fake-nvidia-owned.
func Down(root string) error {
	rootAbs, err := cleanAbsolute(root)
	if err != nil {
		return fmt.Errorf("injection root: %w", err)
	}
	if rootAbs == filepath.Clean(string(os.PathSeparator)) {
		return errors.New("refusing to remove filesystem root")
	}
	if _, err := os.Lstat(rootAbs); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect injection root: %w", err)
	}
	if err := requireOwned(rootAbs); err != nil {
		return err
	}
	if err := os.RemoveAll(rootAbs); err != nil {
		return fmt.Errorf("remove injection root: %w", err)
	}
	return nil
}

func layoutFor(root string) Layout {
	state := filepath.Join(root, "state")
	return Layout{
		Root:          root,
		RuntimeDir:    filepath.Join(root, "runtime"),
		StateDir:      state,
		ConfigPath:    filepath.Join(state, "config.yaml"),
		OverridesPath: filepath.Join(state, "overrides.yaml"),
	}
}

func cleanAbsolute(path string) (string, error) {
	if path == "" {
		return "", errors.New("path is required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func validateSeparation(root, runtimeRoot string) error {
	if root == filepath.Clean(string(os.PathSeparator)) {
		return errors.New("refusing to use filesystem root as injection root")
	}
	if root == runtimeRoot {
		return errors.New("injection root must differ from runtime root")
	}
	if within(root, runtimeRoot) {
		return errors.New("runtime root must not be inside injection root")
	}
	if within(runtimeRoot, root) {
		return errors.New("injection root must not be inside runtime root")
	}
	return nil
}

func within(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil || rel == "." {
		return rel == "."
	}
	return rel != ".." && !filepath.IsAbs(rel) && len(rel) >= 3 && rel[:3] != ".."+string(os.PathSeparator)
}

func requireOwnedOrAbsent(root string) error {
	if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect injection root: %w", err)
	}
	return requireOwned(root)
}

func requireOwned(root string) error {
	data, err := os.ReadFile(filepath.Join(root, markerName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("refusing to replace or remove unowned path %s", root)
		}
		return fmt.Errorf("read injection marker: %w", err)
	}
	if string(data) != markerContent {
		return fmt.Errorf("refusing to replace or remove path with unknown marker %s", root)
	}
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}

		info, err := entry.Info()
		if err != nil {
			return err
		}
		switch {
		case entry.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case entry.Type()&os.ModeSymlink != 0:
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		case info.Mode().IsRegular():
			return copyFile(path, target, info.Mode().Perm())
		default:
			return fmt.Errorf("unsupported runtime artifact type %s (%s)", path, info.Mode())
		}
	})
}

func copyFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(dst)
		}
	}()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}
