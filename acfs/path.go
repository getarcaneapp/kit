package acfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"

	"go.getarcane.app/acfs/pkg/utils"
)

const (
	logicalVolumeRoot = "/volume"
	maxSymlinkHops    = 40
)

func relativePathInternal(components []string) string {
	if len(components) == 0 {
		return "."
	}
	return path.Join(components...)
}

func relativeComponentsInternal(relativePath string) []string {
	if relativePath == "." || relativePath == "" {
		return nil
	}
	return strings.Split(relativePath, "/")
}

func resolvePathInternal(root *os.Root, relativePath string, followFinal bool) (string, error) {
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

		candidate := relativePathInternal(append(resolved, component))
		info, err := root.Lstat(candidate)
		if err != nil {
			return "", fmt.Errorf("lstat %q: %w", utils.LogicalPath(candidate), err)
		}

		isFinal := len(pending) == 0
		if info.Mode()&os.ModeSymlink == 0 || (isFinal && !followFinal) {
			resolved = append(resolved, component)
			continue
		}

		symlinkHops++
		if symlinkHops > maxSymlinkHops {
			return "", ErrSymlinkLoop
		}

		target, err := root.Readlink(candidate)
		if err != nil {
			return "", fmt.Errorf("readlink %q: %w", utils.LogicalPath(candidate), err)
		}

		if strings.HasPrefix(target, "/") {
			switch {
			case target == logicalVolumeRoot:
				target = ""
			case strings.HasPrefix(target, logicalVolumeRoot+"/"):
				target = strings.TrimPrefix(target, logicalVolumeRoot+"/")
			default:
				return "", fmt.Errorf("%w: symlink %q targets %q", ErrOutsideRoot, utils.LogicalPath(candidate), target)
			}
			resolved = resolved[:0]
		}

		targetComponents := strings.Split(target, "/")
		pending = append(targetComponents, pending...)
	}

	return relativePathInternal(resolved), nil
}

func resolveParentInternal(root *os.Root, relativePath string) (string, string, error) {
	parent := path.Dir(relativePath)
	base := path.Base(relativePath)
	if base == "." || base == "/" || base == "" {
		return "", "", ErrInvalidPath
	}

	resolvedParent, err := resolvePathInternal(root, parent, true)
	if err != nil {
		return "", "", err
	}
	return resolvedParent, base, nil
}

func resolveForMkdirInternal(root *os.Root, relativePath string) (string, error) {
	if relativePath == "." {
		return ".", nil
	}

	components := strings.Split(relativePath, "/")
	for index := len(components); index >= 0; index-- {
		prefix := relativePathInternal(components[:index])
		resolvedPrefix, err := resolvePathInternal(root, prefix, true)
		if err == nil {
			resolvedComponents := relativeComponentsInternal(resolvedPrefix)
			return relativePathInternal(append(resolvedComponents, components[index:]...)), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}

	return "", fmt.Errorf("%w: no existing workspace ancestor", ErrInvalidPath)
}
