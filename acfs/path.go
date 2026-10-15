package acfs

import (
	"cmp"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	kitfs "go.getarcane.app/kit/pkg/fs"
)

const (
	logicalVolumeRoot = "/volume"
	maxSymlinkHops    = 40
)

// LogicalPath translates an absolute host path into the logical workspace path
// that every ACFS operation on rootPath expects. It is a purely lexical
// translation — no filesystem access, no symlink resolution — and reports
// ErrOutsideRoot for a path that does not lexically live under rootPath.
func LogicalPath(rootPath, absPath string) (string, error) {
	absoluteRoot, err := filepath.Abs(rootPath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve root %q: %w", ErrInvalidPath, rootPath, err)
	}
	absoluteTarget, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path %q: %w", ErrInvalidPath, absPath, err)
	}

	relative, err := filepath.Rel(absoluteRoot, absoluteTarget)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not relative to %q", ErrOutsideRoot, absPath, rootPath)
	}
	if !filepath.IsLocal(relative) {
		return "", fmt.Errorf("%w: %q is outside %q", ErrOutsideRoot, absPath, rootPath)
	}
	return kitfs.LogicalPath(filepath.ToSlash(relative)), nil
}

func resolvePath(root *os.Root, relativePath string, followFinal bool) (string, error) {
	if relativePath == "." {
		return ".", nil
	}

	pending := strings.Split(relativePath, "/")
	resolved := make([]string, 0, len(pending))
	symlinkHops := 0

	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]

		switch component {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return "", ErrOutsideRoot
			}
			resolved = resolved[:len(resolved)-1]
			continue
		}

		candidate := path.Join(append(resolved, component)...)
		info, err := root.Lstat(candidate)
		if err != nil {
			return "", fmt.Errorf("lstat %q: %w", kitfs.LogicalPath(candidate), err)
		}

		if info.Mode()&os.ModeSymlink == 0 || (len(pending) == 0 && !followFinal) {
			resolved = append(resolved, component)
			continue
		}

		symlinkHops++
		if symlinkHops > maxSymlinkHops {
			return "", ErrSymlinkLoop
		}

		target, err := root.Readlink(candidate)
		if err != nil {
			return "", fmt.Errorf("readlink %q: %w", kitfs.LogicalPath(candidate), err)
		}

		if filepath.IsAbs(target) {
			switch {
			case filepath.Clean(root.Name()) == filepath.VolumeName(root.Name())+string(filepath.Separator):
				// An explicitly filesystem-wide root can resolve host-absolute links.
				relativeTarget, relErr := filepath.Rel(root.Name(), target)
				if relErr != nil {
					return "", fmt.Errorf("%w: symlink %q targets %q", ErrOutsideRoot, kitfs.LogicalPath(candidate), target)
				}
				target = filepath.ToSlash(relativeTarget)
			case target == logicalVolumeRoot:
				target = ""
			case strings.HasPrefix(target, logicalVolumeRoot+"/"):
				target = strings.TrimPrefix(target, logicalVolumeRoot+"/")
			default:
				return "", fmt.Errorf("%w: symlink %q targets %q", ErrOutsideRoot, kitfs.LogicalPath(candidate), target)
			}
			resolved = resolved[:0]
		}

		pending = append(strings.Split(target, "/"), pending...)
	}

	return cmp.Or(path.Join(resolved...), "."), nil
}

func resolveParent(root *os.Root, relativePath string) (string, string, error) {
	base := path.Base(relativePath)
	if base == "." || base == "/" || base == "" {
		return "", "", ErrInvalidPath
	}

	resolvedParent, err := resolvePath(root, path.Dir(relativePath), true)
	if err != nil {
		return "", "", err
	}
	return resolvedParent, base, nil
}
