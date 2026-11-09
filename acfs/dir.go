package acfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"syscall"

	kitfs "kit.getarcane.app/fs"
)

// A dir is opened inside the root without following links, so a mutation naming
// one of its entries cannot be redirected.

type (
	// mutationRoot is a workspace root opened for mutations: root resolves links
	// inside it and top is the same directory as a dir.
	mutationRoot struct {
		root *os.Root
		top  *dir
		path string
	}

	// dirOptions configures openDir.
	dirOptions struct {
		// Mode is the permission set of directories created by Create.
		Mode   os.FileMode
		Create bool
	}
)

var (
	// Test hooks run after a mutation opens a directory and around publishing a
	// staged file.
	afterOpenDir  func(relativePath string)
	beforePublish func(temporaryName string)
	afterPublish  func()
)

func (m *mutationRoot) Close() error {
	if m.root == nil {
		return m.top.Close()
	}
	return errors.Join(m.top.Close(), m.root.Close())
}

// workspace returns the root that resolves links, opening it on first use.
func (m *mutationRoot) workspace() (*os.Root, error) {
	if m.root == nil {
		root, err := os.OpenRoot(m.path)
		if err != nil {
			return nil, fmt.Errorf("open workspace root: %w", err)
		}
		m.root = root
	}
	return m.root, nil
}

// openDir opens relativePath beneath d without following symbolic links. A link
// component reports ErrSymlink and a non-directory one ENOTDIR.
func (d *dir) openDir(relativePath string, options dirOptions) (*dir, error) {
	opened, err := d.open(relativePath, options)
	if err != nil {
		return nil, err
	}
	if afterOpenDir != nil {
		afterOpenDir(opened.path)
	}
	return opened, nil
}

// openParent opens the parent of a mutation target without following symbolic
// links and returns it with the base name.
func (m *mutationRoot) openParent(relativePath string, create bool) (*dir, string, error) {
	parent, err := m.top.openDir(path.Dir(relativePath), dirOptions{Create: create, Mode: applyDirectoryMode})
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, syscall.ENOTDIR) {
		return nil, "", fmt.Errorf("%w: %s", ErrNotDirectory, pathErr.Path)
	}
	if err != nil {
		return nil, "", err
	}
	return parent, path.Base(relativePath), nil
}

// openResolvedParent resolves the parent through links inside the root, then
// opens it without following any. notDirectory builds the error for a non-directory.
func (m *mutationRoot) openResolvedParent(relativePath string, notDirectory func(target string) error) (*dir, string, error) {
	if relativePath != "." {
		if parent, fastErr := m.top.openDir(path.Dir(relativePath), dirOptions{}); fastErr == nil {
			return parent, path.Base(relativePath), nil
		}
	}
	root, err := m.workspace()
	if err != nil {
		return nil, "", err
	}
	resolvedParent, base, err := resolveParent(root, relativePath)
	if err != nil {
		return nil, "", err
	}
	parent, err := m.top.openDir(resolvedParent, dirOptions{})
	if errors.Is(err, syscall.ENOTDIR) {
		return nil, "", notDirectory(path.Join(resolvedParent, base))
	}
	if err != nil {
		return nil, "", err
	}
	return parent, base, nil
}

// unwrapPathError returns the error inside a *fs.PathError, like the os package.
func unwrapPathError(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err
	}
	return err
}

// display renders name in d for error messages.
func (d *dir) display(name string) string {
	if d.host != "" {
		return filepath.Join(d.host, name)
	}
	return kitfs.LogicalPath(path.Join(d.path, name))
}

// errorPath renders name in d for *fs.PathError and *os.LinkError, as os.Root does.
func (d *dir) errorPath(name string) string {
	if d.host != "" {
		return filepath.Join(d.host, name)
	}
	return path.Join(d.path, name)
}

// verifyPublished reports ErrReplaced unless name in d is still the staged file.
func (d *dir) verifyPublished(name string, staged *os.File) error {
	same, err := d.isFile(name, staged)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrReplaced, d.display(name), err)
	}
	if !same {
		return fmt.Errorf("%w: %s", ErrReplaced, d.display(name))
	}
	return nil
}

// rejectExistingTarget reports ErrAlreadyExists, or ErrSymlink for a link, when
// name exists in d.
func rejectExistingTarget(d *dir, name string) error {
	info, err := d.Lstat(name)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrSymlink
		}
		return ErrAlreadyExists
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
