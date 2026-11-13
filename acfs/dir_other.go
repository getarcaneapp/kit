//go:build !linux

package acfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"time"
)

// Outside Linux, dir wraps an *os.Root and checks each name with Lstat before
// using it, which does not close the race with a concurrent writer.

// dir is a directory inside a mutationRoot, or a Host directory.
type dir struct {
	root *os.Root
	// workspace is the workspace root, nil for a Host directory.
	workspace *os.Root
	// path is the directory's lexical path relative to the root when opened.
	path string
	// host is the directory's host path for a Host directory, else empty.
	host string
}

func statWithoutLinks(string, string) (os.FileInfo, bool) {
	return nil, false
}

func openMutationRoot(rootPath string) (*mutationRoot, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	top, err := root.OpenRoot(".")
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	return &mutationRoot{
		root: root,
		path: rootPath,
		top: &dir{
			root:      top,
			workspace: root,
			path:      ".",
		},
	}, nil
}

// openHostDir opens the host directory hostPath, following symbolic links.
func openHostDir(hostPath string) (*dir, error) {
	root, err := os.OpenRoot(hostPath)
	if err != nil {
		return nil, err
	}
	if afterOpenDir != nil {
		afterOpenDir(hostPath)
	}
	return &dir{
		root: root,
		path: ".",
		host: hostPath,
	}, nil
}

func (d *dir) Close() error {
	return d.root.Close()
}

// rootError rewrites paths os.Root reports relative to d as the Linux
// implementation reports them.
func (d *dir) rootError(err error) error {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return &fs.PathError{
			Op:   pathErr.Op,
			Path: d.errorPath(pathErr.Path),
			Err:  pathErr.Err,
		}
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return &os.LinkError{
			Op:  linkErr.Op,
			Old: d.errorPath(linkErr.Old),
			New: d.errorPath(linkErr.New),
			Err: linkErr.Err,
		}
	}
	return err
}

func (d *dir) open(relativePath string, options dirOptions) (*dir, error) {
	current, err := d.root.OpenRoot(".")
	if err != nil {
		return nil, &fs.PathError{
			Op:   "openat",
			Path: d.errorPath("."),
			Err:  unwrapPathError(err),
		}
	}
	if relativePath == "." {
		return &dir{
			root:      current,
			workspace: d.workspace,
			path:      d.path,
			host:      d.host,
		}, nil
	}
	walked := ""
	for component := range strings.SplitSeq(relativePath, "/") {
		walked = path.Join(walked, component)
		next, walkErr := d.walkComponent(current, component, walked, options)
		_ = current.Close()
		if walkErr != nil {
			return nil, walkErr
		}
		current = next
	}
	return &dir{
		root:      current,
		workspace: d.workspace,
		path:      path.Join(d.path, relativePath),
		host:      d.host,
	}, nil
}

func (d *dir) walkComponent(parent *os.Root, name, walked string, options dirOptions) (*os.Root, error) {
	errorPath := d.errorPath(walked)
	info, err := parent.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) && options.Create {
		err = parent.Mkdir(name, options.Mode)
		if err != nil && !errors.Is(err, fs.ErrExist) {
			return nil, &fs.PathError{
				Op:   "mkdirat",
				Path: errorPath,
				Err:  unwrapPathError(err),
			}
		}
		info, err = parent.Lstat(name)
	}
	switch {
	case err != nil:
		return nil, &fs.PathError{
			Op:   "openat",
			Path: errorPath,
			Err:  unwrapPathError(err),
		}
	case info.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("%w: %s", ErrSymlink, d.display(walked))
	case !info.IsDir():
		return nil, &fs.PathError{
			Op:   "openat",
			Path: errorPath,
			Err:  syscall.ENOTDIR,
		}
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, &fs.PathError{
			Op:   "openat",
			Path: errorPath,
			Err:  unwrapPathError(err),
		}
	}
	return child, nil
}

func (d *dir) sameAs(other *dir) (bool, error) {
	current, err := d.root.Stat(".")
	if err != nil {
		return false, err
	}
	live, err := other.root.Stat(".")
	if err != nil {
		return false, err
	}
	return os.SameFile(current, live), nil
}

// isFile reports whether name in d, not followed, is the open file f.
func (d *dir) isFile(name string, f *os.File) (bool, error) {
	entry, err := d.root.Lstat(name)
	if err != nil {
		return false, d.rootError(err)
	}
	opened, err := f.Stat()
	if err != nil {
		return false, err
	}
	return os.SameFile(entry, opened), nil
}

// refuseSymlink reports ErrSymlink when name is a symbolic link.
func (d *dir) refuseSymlink(name string) error {
	info, err := d.root.Lstat(name)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrSymlink, d.display(name))
	}
	return nil
}

func (d *dir) Lstat(name string) (os.FileInfo, error) {
	info, err := d.root.Lstat(name)
	return info, d.rootError(err)
}

// specialModeBits are refused by os.Root on create, so OpenFile chmods them after.
const specialModeBits = os.ModeSetuid | os.ModeSetgid | os.ModeSticky

func (d *dir) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	if err := d.refuseSymlink(name); err != nil {
		return nil, err
	}
	if flag&os.O_CREATE == 0 || perm&specialModeBits == 0 {
		file, err := d.root.OpenFile(name, flag, perm)
		return file, d.rootError(err)
	}
	file, err := d.root.OpenFile(name, flag|os.O_EXCL, perm.Perm())
	if errors.Is(err, fs.ErrExist) && flag&os.O_EXCL == 0 {
		file, err = d.root.OpenFile(name, flag&^os.O_CREATE, perm.Perm())
		return file, d.rootError(err)
	}
	if err != nil {
		return nil, d.rootError(err)
	}
	if err := file.Chmod(perm); err != nil {
		_ = file.Close()
		return nil, d.rootError(err)
	}
	return file, nil
}

func (d *dir) Readlink(name string) (string, error) {
	target, err := d.root.Readlink(name)
	return target, d.rootError(err)
}

func (d *dir) Mkdir(name string, mode os.FileMode) error {
	return d.rootError(d.root.Mkdir(name, mode))
}

func (d *dir) Symlink(target, name string) error {
	return d.rootError(d.root.Symlink(target, name))
}

func (d *dir) Link(oldname, newname string) error {
	return d.rootError(d.root.Link(oldname, newname))
}

func (d *dir) Remove(name string) error {
	return d.rootError(d.root.Remove(name))
}

func (d *dir) RemoveAll(name string) error {
	return d.rootError(d.root.RemoveAll(name))
}

func (d *dir) renameTo(oldname string, target *dir, newname string) error {
	if d.workspace == nil {
		return d.rootError(d.root.Rename(oldname, newname))
	}
	return d.workspace.Rename(path.Join(d.path, oldname), path.Join(target.path, newname))
}

// renameNoReplace relies on callers refusing an existing target first.
func (d *dir) renameNoReplace(oldname string, target *dir, newname string) error {
	return d.renameTo(oldname, target, newname)
}

func (d *dir) Chmod(name string, mode os.FileMode) error {
	if err := d.refuseSymlink(name); err != nil {
		return err
	}
	return d.rootError(d.root.Chmod(name, mode))
}

func (d *dir) Chown(name string, uid, gid int) error {
	if err := d.refuseSymlink(name); err != nil {
		return err
	}
	return d.rootError(d.root.Chown(name, uid, gid))
}

func (d *dir) Lchown(name string, uid, gid int) error {
	return d.rootError(d.root.Lchown(name, uid, gid))
}

func (d *dir) Chtimes(name string, atime, mtime time.Time) error {
	if err := d.refuseSymlink(name); err != nil {
		return err
	}
	return d.rootError(d.root.Chtimes(name, atime, mtime))
}
