package acfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"kit.getarcane.app"
	kitfs "kit.getarcane.app/fs"
)

// Symlink creates a root-confined symbolic link whose target must stay inside the
// root, storing an absolute target as the equivalent relative path.
func (r Root) Symlink(ctx context.Context, target, linkPath string) (retErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	if target == "" || strings.ContainsRune(target, '\x00') {
		return fmt.Errorf("%w: invalid symlink target %q", ErrInvalidPath, target)
	}
	relativePath, err := normalizeMutationPath(linkPath)
	if err != nil {
		return err
	}

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()

	parent, base, err := mutation.openParent(relativePath, false)
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	root, err := mutation.workspace()
	if err != nil {
		return err
	}
	storedTarget, err := confinedLinkTarget(root, parent.path, target)
	if err != nil {
		return err
	}
	err = parent.Symlink(storedTarget, base)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %q: %w", ErrAlreadyExists, linkPath, fs.ErrExist)
	}
	if err != nil {
		return fmt.Errorf("symlink %q: %w", linkPath, err)
	}
	// Remove the link if parent moved after the target was checked.
	defer func() {
		if retErr == nil {
			return
		}
		if stored, readErr := parent.Readlink(base); readErr == nil && stored == storedTarget {
			_ = parent.Remove(base)
		}
	}()
	current, err := mutation.top.openDir(parent.path, dirOptions{})
	if err != nil {
		return fmt.Errorf("symlink %q: %w: %s moved: %w", linkPath, ErrOutsideRoot, kitfs.LogicalPath(parent.path), err)
	}
	defer func() {
		_ = current.Close()
	}()
	same, err := parent.sameAs(current)
	if err != nil {
		return fmt.Errorf("symlink %q: %w", linkPath, err)
	}
	if !same {
		return fmt.Errorf("symlink %q: %w: %s moved", linkPath, ErrOutsideRoot, kitfs.LogicalPath(parent.path))
	}
	return nil
}

// confinedLinkTarget checks that target stays inside root and returns the target
// to store.
func confinedLinkTarget(root *os.Root, parent, target string) (string, error) {
	// A native separator or volume name would resolve differently on the host.
	if filepath.Separator != '/' && strings.ContainsRune(target, filepath.Separator) {
		return "", fmt.Errorf("%w: symlink target %q uses %q", ErrInvalidPath, target, filepath.Separator)
	}
	if filepath.VolumeName(target) != "" {
		return "", fmt.Errorf("%w: symlink target %q", ErrOutsideRoot, target)
	}
	absolute := strings.HasPrefix(target, "/")
	base := kit.Ternary(absolute, ".", parent)
	unresolved := kit.Ternary(absolute, strings.TrimLeft(target, "/"), target)
	if absolute {
		// The stored relative form only matches the kernel if no ".." follows a link.
		walked := make([]string, 0, strings.Count(unresolved, "/")+1)
		for component := range strings.SplitSeq(unresolved, "/") {
			switch component {
			case "", ".":
				continue
			case "..":
				if len(walked) == 0 {
					continue
				}
				prefix := path.Join(walked...)
				info, err := root.Lstat(prefix)
				if err == nil && info.Mode()&os.ModeSymlink != 0 {
					return "", fmt.Errorf("symlink target %q: %w: \"..\" follows symbolic link %q", target, ErrInvalidPath, kitfs.LogicalPath(prefix))
				}
				walked = walked[:len(walked)-1]
			default:
				walked = append(walked, component)
			}
		}
	}

	lexical := path.Join(base, unresolved)
	if lexical == ".." || strings.HasPrefix(lexical, "../") {
		return "", fmt.Errorf("%w: symlink target %q", ErrOutsideRoot, target)
	}
	if err := rejectReservedPath(lexical); err != nil {
		return "", err
	}
	// The kernel applies ".." after following links, so resolve through existing links.
	_, _, err := resolvePath(root, base+"/"+unresolved, resolveOptions{FollowFinal: true, HostEquivalent: true})
	if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
		return "", fmt.Errorf("symlink target %q: %w", target, err)
	}

	if !absolute {
		return target, nil
	}
	relative, err := filepath.Rel(parent, lexical)
	if err != nil {
		return "", fmt.Errorf("%w: symlink target %q: %w", ErrInvalidPath, target, err)
	}
	return filepath.ToSlash(relative), nil
}

// Readlink returns the stored target of a root-confined symbolic link verbatim.
func (r Root) Readlink(ctx context.Context, logicalPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return "", err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return "", err
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return "", fmt.Errorf("open workspace root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()

	resolvedParent, base, err := resolveParent(root, relativePath)
	if err != nil {
		return "", err
	}
	target, err := root.Readlink(path.Join(resolvedParent, base))
	if err != nil {
		return "", fmt.Errorf("readlink %q: %w", logicalPath, err)
	}
	return target, nil
}

// Symlink creates a host symbolic link storing target verbatim. An existing
// linkPath reports ErrAlreadyExists.
func (Host) Symlink(ctx context.Context, target, linkPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if target == "" || strings.ContainsRune(target, '\x00') {
		return fmt.Errorf("%w: invalid symlink target %q", ErrInvalidPath, target)
	}
	link, err := cleanHostPath(linkPath)
	if err != nil {
		return err
	}
	err = os.Symlink(target, link)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %q: %w", ErrAlreadyExists, link, fs.ErrExist)
	}
	if err != nil {
		return fmt.Errorf("symlink %q: %w", link, err)
	}
	return nil
}

// Readlink returns the stored target of a host symbolic link.
func (Host) Readlink(ctx context.Context, hostPath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	link, err := cleanHostPath(hostPath)
	if err != nil {
		return "", err
	}
	target, err := os.Readlink(link)
	if err != nil {
		return "", fmt.Errorf("readlink %q: %w", link, err)
	}
	return target, nil
}
