package acfs

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	kitfs "kit.getarcane.app/fs"
)

type (
	// resolveOptions configures resolvePath.
	resolveOptions struct {
		// FollowFinal follows a symbolic link in the final component.
		FollowFinal bool
		// HostEquivalent refuses with ErrOutsideRoot any link that resolves differently
		// on the host, such as an absolute target.
		HostEquivalent bool
	}
)

const (
	logicalVolumeRoot = "/volume"
	maxSymlinkHops    = 40
)

// LogicalPath lexically translates an absolute host path under r.Path into a
// logical path, reporting ErrOutsideRoot for one outside it.
func (r Root) LogicalPath(absPath string) (string, error) {
	if r.Path == "" {
		return "", fmt.Errorf("%w: root path is empty", ErrInvalidPath)
	}
	absoluteRoot, err := filepath.Abs(r.Path)
	if err != nil {
		return "", fmt.Errorf("%w: resolve root %q: %w", ErrInvalidPath, r.Path, err)
	}
	absoluteTarget, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("%w: resolve path %q: %w", ErrInvalidPath, absPath, err)
	}

	relative, err := filepath.Rel(absoluteRoot, absoluteTarget)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not relative to %q", ErrOutsideRoot, absPath, r.Path)
	}
	if !filepath.IsLocal(relative) {
		return "", fmt.Errorf("%w: %q is outside %q", ErrOutsideRoot, absPath, r.Path)
	}
	return kitfs.LogicalPath(filepath.ToSlash(relative)), nil
}

// resolvePath resolves relativePath through links inside root, returning the
// final Lstat result or nil when resolution ended on the root, "..", or a link.
func resolvePath(root *os.Root, relativePath string, options resolveOptions) (string, os.FileInfo, error) {
	if relativePath == "." {
		return ".", nil, nil
	}

	pending := strings.Split(relativePath, "/")
	resolved := make([]string, 0, len(pending))
	var info os.FileInfo
	symlinkHops := 0

	for len(pending) > 0 {
		component := pending[0]
		pending = pending[1:]

		switch component {
		case "", ".":
			continue
		case "..":
			if len(resolved) == 0 {
				return "", nil, ErrOutsideRoot
			}
			resolved = resolved[:len(resolved)-1]
			info = nil
			continue
		}

		candidate := path.Join(append(resolved, component)...)
		var err error
		info, err = root.Lstat(candidate)
		if err != nil && options.HostEquivalent && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)) {
			depth := len(resolved) + 1
			for _, remaining := range pending {
				switch remaining {
				case "", ".":
				case "..":
					depth--
				default:
					depth++
				}
				if depth < 0 {
					return "", nil, ErrOutsideRoot
				}
			}
		}
		if err != nil {
			return "", nil, fmt.Errorf("lstat %q: %w", kitfs.LogicalPath(candidate), err)
		}

		if info.Mode()&os.ModeSymlink == 0 || (len(pending) == 0 && !options.FollowFinal) {
			resolved = append(resolved, component)
			continue
		}

		symlinkHops++
		if symlinkHops > maxSymlinkHops {
			return "", nil, ErrSymlinkLoop
		}

		target, err := root.Readlink(candidate)
		if err != nil {
			return "", nil, fmt.Errorf("readlink %q: %w", kitfs.LogicalPath(candidate), err)
		}
		info = nil

		if filepath.IsAbs(target) {
			switch {
			case filepath.Clean(root.Name()) == filepath.VolumeName(root.Name())+string(filepath.Separator):
				relativeTarget, relErr := filepath.Rel(root.Name(), target)
				if relErr != nil {
					return "", nil, fmt.Errorf("%w: symlink %q targets %q", ErrOutsideRoot, kitfs.LogicalPath(candidate), target)
				}
				target = filepath.ToSlash(relativeTarget)
			case options.HostEquivalent:
				return "", nil, fmt.Errorf("%w: symlink %q targets host path %q", ErrOutsideRoot, kitfs.LogicalPath(candidate), target)
			case target == logicalVolumeRoot:
				target = ""
			case strings.HasPrefix(target, logicalVolumeRoot+"/"):
				target = strings.TrimPrefix(target, logicalVolumeRoot+"/")
			default:
				return "", nil, fmt.Errorf("%w: symlink %q targets %q", ErrOutsideRoot, kitfs.LogicalPath(candidate), target)
			}
			resolved = resolved[:0]
		}

		pending = append(strings.Split(target, "/"), pending...)
	}

	return cmp.Or(path.Join(resolved...), "."), info, nil
}

func resolveParent(root *os.Root, relativePath string) (string, string, error) {
	base := path.Base(relativePath)
	if base == "." || base == "/" || base == "" {
		return "", "", ErrInvalidPath
	}

	resolvedParent, _, err := resolvePath(root, path.Dir(relativePath), resolveOptions{FollowFinal: true})
	if err != nil {
		return "", "", err
	}
	return resolvedParent, base, nil
}
