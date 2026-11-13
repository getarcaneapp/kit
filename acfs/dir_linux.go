//go:build linux

package acfs

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"kit.getarcane.app"
)

type (
	// dir is a directory held open by descriptor.
	dir struct {
		rootName string
		// path is the directory's lexical path relative to the root when opened.
		path string
		// host is the directory's host path for a Host directory, else empty.
		host string
		fd   int
		// borrowed marks a descriptor owned by another dir, which Close leaves open.
		borrowed bool
	}

	// statInfo is the os.FileInfo of a raw Stat_t, decoded like os.Lstat. It holds
	// a syscall.Stat_t so Sys matches os.Lstat.
	statInfo struct {
		name string
		stat syscall.Stat_t
	}
)

const (
	removeAllBatchSize = 1024
	// noFollowResolve refuses every symbolic link, magic links, and escapes.
	noFollowResolve = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
)

var (
	// openat2Available reports whether openat2 (Linux 5.6+) is usable.
	openat2Available = sync.OnceValue(func() bool {
		fd, err := unix.Openat2(unix.AT_FDCWD, ".", &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC, Resolve: noFollowResolve})
		if err != nil {
			return false
		}
		_ = unix.Close(fd)
		return true
	})

	// forceOpenatWalk makes tests use the openat walk.
	forceOpenatWalk bool

	// renameat2 is replaced by tests to simulate missing RENAME_NOREPLACE.
	renameat2 = unix.Renameat2
)

func openMutationRoot(rootPath string) (*mutationRoot, error) {
	fd, err := ignoringEINTR(func() (int, error) {
		return unix.Open(rootPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", &fs.PathError{Op: "open", Path: rootPath, Err: err})
	}
	return &mutationRoot{
		top: &dir{
			rootName: rootPath,
			path:     ".",
			fd:       fd,
		},
		path: rootPath,
	}, nil
}

// openHostDir opens the host directory hostPath, following symbolic links.
func openHostDir(hostPath string) (*dir, error) {
	fd, err := ignoringEINTR(func() (int, error) {
		return unix.Open(hostPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: hostPath, Err: err}
	}
	if afterOpenDir != nil {
		afterOpenDir(hostPath)
	}
	return &dir{rootName: hostPath, path: ".", host: hostPath, fd: fd}, nil
}

// statWithoutLinks stats rootPath/relativePath in one openat2 that refuses
// every symbolic link, so the result cannot leave the root. It reports false
// for anything the full resolver must handle, including errors.
func statWithoutLinks(rootPath, relativePath string) (os.FileInfo, bool) {
	if rootPath == "" || relativePath == "." || forceOpenatWalk || !openat2Available() {
		return nil, false
	}
	fd, err := ignoringEINTR(func() (int, error) {
		return unix.Openat2(unix.AT_FDCWD, rootPath+"/"+relativePath, &unix.OpenHow{
			Flags:   unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC,
			Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
		})
	})
	if err != nil {
		return nil, false
	}
	var st syscall.Stat_t
	err = syscall.Fstat(fd, &st)
	_ = unix.Close(fd)
	if err != nil || st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return nil, false
	}
	return statInfo{
		name: path.Base(relativePath),
		stat: st,
	}, true
}

func (i statInfo) Name() string {
	return i.name
}

func (i statInfo) Size() int64 {
	return i.stat.Size
}

func (i statInfo) Mode() os.FileMode {
	mode := os.FileMode(i.stat.Mode & 0o777)
	switch i.stat.Mode & unix.S_IFMT {
	case unix.S_IFBLK:
		mode |= os.ModeDevice
	case unix.S_IFCHR:
		mode |= os.ModeDevice | os.ModeCharDevice
	case unix.S_IFDIR:
		mode |= os.ModeDir
	case unix.S_IFIFO:
		mode |= os.ModeNamedPipe
	case unix.S_IFSOCK:
		mode |= os.ModeSocket
	}
	if i.stat.Mode&unix.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if i.stat.Mode&unix.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if i.stat.Mode&unix.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}
	return mode
}

func (i statInfo) ModTime() time.Time {
	return time.Unix(i.stat.Mtim.Unix())
}

func (i statInfo) IsDir() bool {
	return i.stat.Mode&unix.S_IFMT == unix.S_IFDIR
}

func (i statInfo) Sys() any {
	return &i.stat
}

func (d *dir) Close() error {
	if d.borrowed {
		return nil
	}
	return unix.Close(d.fd)
}

// open opens relativePath with openat2, falling back to the component walk on
// any failure, including EAGAIN after a concurrent rename.
func (d *dir) open(relativePath string, options dirOptions) (*dir, error) {
	if relativePath == "." {
		return &dir{rootName: d.rootName, path: d.path, host: d.host, fd: d.fd, borrowed: true}, nil
	}
	if !forceOpenatWalk && openat2Available() {
		fd, err := ignoringEINTR(func() (int, error) {
			return unix.Openat2(d.fd, relativePath, &unix.OpenHow{
				Flags:   unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC,
				Resolve: noFollowResolve,
			})
		})
		if err == nil {
			return &dir{
				rootName: d.rootName,
				path:     path.Join(d.path, relativePath),
				host:     d.host,
				fd:       fd,
			}, nil
		}
	}
	return d.walk(relativePath, options)
}

// walk opens relativePath one component at a time without following links.
func (d *dir) walk(relativePath string, options dirOptions) (*dir, error) {
	fd := d.fd
	walked := ""
	for component := range strings.SplitSeq(relativePath, "/") {
		walked = path.Join(walked, component)
		next, walkErr := d.walkComponent(fd, component, walked, options)
		if fd != d.fd {
			_ = unix.Close(fd)
		}
		if walkErr != nil {
			return nil, walkErr
		}
		fd = next
	}
	return &dir{
		rootName: d.rootName,
		path:     path.Join(d.path, relativePath),
		host:     d.host,
		fd:       fd,
	}, nil
}

// walkComponent opens the directory name inside parent without following it.
func (d *dir) walkComponent(parent int, name, walked string, options dirOptions) (int, error) {
	open := func() (int, error) {
		return ignoringEINTR(func() (int, error) {
			return unix.Openat(parent, name, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		})
	}
	fd, err := open()
	if errors.Is(err, unix.ENOENT) && options.Create {
		err = unix.Mkdirat(parent, name, syscallMode(options.Mode))
		if err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, &fs.PathError{Op: "mkdirat", Path: d.errorPath(walked), Err: err}
		}
		fd, err = open()
	}
	// O_DIRECTORY reports ENOTDIR for a link opened with O_PATH|O_NOFOLLOW.
	if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
		var stat unix.Stat_t
		if unix.Fstatat(parent, name, &stat, unix.AT_SYMLINK_NOFOLLOW) == nil && stat.Mode&unix.S_IFMT == unix.S_IFLNK {
			return -1, fmt.Errorf("%w: %s", ErrSymlink, d.display(walked))
		}
		return -1, &fs.PathError{Op: "openat", Path: d.errorPath(walked), Err: syscall.ENOTDIR}
	}
	if err != nil {
		return -1, &fs.PathError{Op: "openat", Path: d.errorPath(walked), Err: err}
	}
	return fd, nil
}

func (d *dir) sameAs(other *dir) (bool, error) {
	var current, live unix.Stat_t
	if err := unix.Fstat(d.fd, &current); err != nil {
		return false, &fs.PathError{Op: "fstat", Path: d.errorPath("."), Err: err}
	}
	if err := unix.Fstat(other.fd, &live); err != nil {
		return false, &fs.PathError{Op: "fstat", Path: other.errorPath("."), Err: err}
	}
	return current.Dev == live.Dev && current.Ino == live.Ino, nil
}

// isFile reports whether name in d, not followed, is the open file f.
func (d *dir) isFile(name string, f *os.File) (bool, error) {
	var entry, opened unix.Stat_t
	err := unix.Fstatat(d.fd, name, &entry, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil {
		return false, &fs.PathError{Op: "statat", Path: d.errorPath(name), Err: err}
	}
	err = unix.Fstat(int(f.Fd()), &opened)
	if err != nil {
		return false, &fs.PathError{Op: "fstat", Path: f.Name(), Err: err}
	}
	return entry.Dev == opened.Dev && entry.Ino == opened.Ino, nil
}

// Lstat returns metadata for name without following it.
func (d *dir) Lstat(name string) (os.FileInfo, error) {
	fd, err := ignoringEINTR(func() (int, error) {
		return unix.Openat(d.fd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return nil, &fs.PathError{Op: "statat", Path: d.errorPath(name), Err: err}
	}
	file := os.NewFile(uintptr(fd), name)
	defer func() {
		_ = file.Close()
	}()
	return file.Stat()
}

// OpenFile opens name without following it. O_NONBLOCK keeps a swapped-in FIFO
// from blocking and is cleared before returning.
func (d *dir) OpenFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	fd, err := ignoringEINTR(func() (int, error) {
		return unix.Openat(d.fd, name, flag|unix.O_NOFOLLOW|unix.O_CLOEXEC|kit.Ternary(flag&unix.O_EXCL == 0, unix.O_NONBLOCK, 0), syscallMode(perm))
	})
	switch {
	case errors.Is(err, unix.ELOOP):
		return nil, fmt.Errorf("%w: %s", ErrSymlink, d.display(name))
	case errors.Is(err, unix.ENXIO):
		return nil, fmt.Errorf("%w: %s", ErrNotFile, d.display(name))
	case err != nil:
		return nil, &fs.PathError{Op: "openat", Path: d.errorPath(name), Err: err}
	}
	if flag&unix.O_EXCL == 0 {
		if err = unix.SetNonblock(fd, false); err != nil {
			_ = unix.Close(fd)
			return nil, &fs.PathError{Op: "fcntl", Path: d.errorPath(name), Err: err}
		}
	}
	if d.host != "" {
		return os.NewFile(uintptr(fd), filepath.Join(d.host, name)), nil
	}
	return os.NewFile(uintptr(fd), d.rootName+"/"+path.Join(d.path, name)), nil
}

func (d *dir) Readlink(name string) (string, error) {
	for size := 128; ; size *= 2 {
		buffer := make([]byte, size)
		n, err := unix.Readlinkat(d.fd, name, buffer)
		if err != nil {
			return "", &fs.PathError{Op: "readlinkat", Path: d.errorPath(name), Err: err}
		}
		if n < size {
			return string(buffer[:n]), nil
		}
	}
}

func (d *dir) Mkdir(name string, mode os.FileMode) error {
	err := unix.Mkdirat(d.fd, name, syscallMode(mode))
	if err != nil {
		return &fs.PathError{Op: "mkdirat", Path: d.errorPath(name), Err: err}
	}
	return nil
}

func (d *dir) Symlink(target, name string) error {
	err := unix.Symlinkat(target, d.fd, name)
	if err != nil {
		return &os.LinkError{Op: "symlinkat", Old: target, New: d.errorPath(name), Err: err}
	}
	return nil
}

func (d *dir) Link(oldname, newname string) error {
	err := unix.Linkat(d.fd, oldname, d.fd, newname, 0)
	if err != nil {
		return &os.LinkError{Op: "linkat", Old: d.errorPath(oldname), New: d.errorPath(newname), Err: err}
	}
	return nil
}

// Remove removes a file or empty directory like os.Remove.
func (d *dir) Remove(name string) error {
	err := unix.Unlinkat(d.fd, name, 0)
	if err == nil {
		return nil
	}
	directoryErr := unix.Unlinkat(d.fd, name, unix.AT_REMOVEDIR)
	if directoryErr == nil {
		return nil
	}
	// rmdir of a non-directory reports ENOTDIR, so the unlink error is the real one.
	if !errors.Is(directoryErr, unix.ENOTDIR) {
		err = directoryErr
	}
	return &fs.PathError{Op: "removeat", Path: d.errorPath(name), Err: err}
}

// RemoveAll removes name and its children like os.RemoveAll without following links.
func (d *dir) RemoveAll(name string) error {
	err := removeAllAt(d.fd, name, d.errorPath(name))
	if err != nil {
		return &fs.PathError{Op: "RemoveAll", Path: d.errorPath(name), Err: unwrapPathError(err)}
	}
	return nil
}

// removeAllAt follows os.RemoveAll's descriptor-relative algorithm.
func removeAllAt(parent int, name, display string) error {
	unlinkErr := unix.Unlinkat(parent, name, 0)
	if unlinkErr == nil || errors.Is(unlinkErr, unix.ENOENT) {
		return nil
	}
	// EPERM or EACCES may still be a directory whose contents can be removed.
	if !errors.Is(unlinkErr, unix.EISDIR) && !errors.Is(unlinkErr, unix.EPERM) && !errors.Is(unlinkErr, unix.EACCES) {
		return &fs.PathError{Op: "unlinkat", Path: display, Err: unlinkErr}
	}

	var childErr error
	for {
		fd, err := ignoringEINTR(func() (int, error) {
			return unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		})
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		if errors.Is(err, unix.ENOTDIR) || errors.Is(err, unix.ELOOP) {
			return &fs.PathError{Op: "unlinkat", Path: display, Err: unlinkErr}
		}
		if err != nil {
			childErr = &fs.PathError{Op: "openat", Path: display, Err: err}
			break
		}

		directory := os.NewFile(uintptr(fd), display)
		read := 0
		for {
			names, readErr := directory.Readdirnames(removeAllBatchSize)
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				_ = directory.Close()
				if errors.Is(readErr, fs.ErrNotExist) {
					return nil
				}
				return &fs.PathError{Op: "readdirnames", Path: display, Err: readErr}
			}
			read = len(names)
			failed := 0
			for _, child := range names {
				if removeErr := removeAllAt(fd, child, display+"/"+child); removeErr != nil {
					failed++
					childErr = cmp.Or(childErr, removeErr)
				}
			}
			// Re-open the directory unless no entry in the batch could be removed.
			if failed != removeAllBatchSize {
				break
			}
		}
		_ = directory.Close()
		if read < removeAllBatchSize {
			break
		}
	}

	err := unix.Unlinkat(parent, name, unix.AT_REMOVEDIR)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return nil
	}
	if childErr != nil {
		return childErr
	}
	return &fs.PathError{Op: "unlinkat", Path: display, Err: err}
}

// renameTo renames oldname in d to newname in target like os.Rename.
func (d *dir) renameTo(oldname string, target *dir, newname string) error {
	err := renameat2(d.fd, oldname, target.fd, newname, unix.RENAME_NOREPLACE)
	if err == nil {
		return nil
	}
	var targetStat, sourceStat unix.Stat_t
	err = nil
	if unix.Fstatat(target.fd, newname, &targetStat, unix.AT_SYMLINK_NOFOLLOW) == nil && targetStat.Mode&unix.S_IFMT == unix.S_IFDIR {
		err = unix.Fstatat(d.fd, oldname, &sourceStat, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil && (newname == oldname || sourceStat.Dev != targetStat.Dev || sourceStat.Ino != targetStat.Ino) {
			err = unix.EEXIST
		}
	}
	if err == nil {
		err = unix.Renameat(d.fd, oldname, target.fd, newname)
	}
	if err != nil {
		return &os.LinkError{Op: "renameat", Old: d.errorPath(oldname), New: target.errorPath(newname), Err: err}
	}
	return nil
}

// renameNoReplace renames without replacing newname. Without RENAME_NOREPLACE a
// file is linked then unlinked and a directory reports errors.ErrUnsupported.
func (d *dir) renameNoReplace(oldname string, target *dir, newname string) error {
	renameErr := renameat2(d.fd, oldname, target.fd, newname, unix.RENAME_NOREPLACE)
	err := renameErr
	if errors.Is(renameErr, unix.ENOSYS) || errors.Is(renameErr, unix.EINVAL) || errors.Is(renameErr, unix.EPERM) {
		var stat unix.Stat_t
		err = unix.Fstatat(d.fd, oldname, &stat, unix.AT_SYMLINK_NOFOLLOW)
		if err == nil && stat.Mode&unix.S_IFMT == unix.S_IFDIR {
			err = fmt.Errorf("rename directory without replacing: %w", errors.ErrUnsupported)
		}
		if err == nil {
			err = unix.Linkat(d.fd, oldname, target.fd, newname, 0)
			if err == nil {
				err = unix.Unlinkat(d.fd, oldname, 0)
				if err != nil {
					_ = unix.Unlinkat(target.fd, newname, 0)
				}
			}
		}
		if errors.Is(renameErr, unix.EPERM) && (errors.Is(err, unix.EPERM) || errors.Is(err, unix.EACCES) || errors.Is(err, errors.ErrUnsupported)) {
			err = renameErr
		}
	}
	if err != nil {
		return &os.LinkError{Op: "renameat", Old: d.errorPath(oldname), New: target.errorPath(newname), Err: err}
	}
	return nil
}

// Attribute changes act on an O_PATH|O_NOFOLLOW descriptor, so a link swapped
// in afterwards is never followed.

// Chmod uses fchmodat2 with AT_EMPTY_PATH (Linux 6.6+), falling back to the
// /proc/self/fd magic link on EOPNOTSUPP or a seccomp EPERM.
func (d *dir) Chmod(name string, mode os.FileMode) error {
	return d.changeTarget("chmodat", name, false, func(fd int) error {
		err := unix.Fchmodat(fd, "", syscallMode(mode), unix.AT_EMPTY_PATH)
		// Linux 6.5 has fchmodat2 but rejects AT_EMPTY_PATH with EINVAL.
		if errors.Is(err, unix.EOPNOTSUPP) || errors.Is(err, unix.EPERM) || errors.Is(err, unix.EINVAL) {
			return unix.Fchmodat(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), syscallMode(mode), 0)
		}
		return err
	})
}

func (d *dir) Chown(name string, uid, gid int) error {
	return d.changeTarget("chownat", name, false, func(fd int) error {
		return unix.Fchownat(fd, "", uid, gid, unix.AT_EMPTY_PATH)
	})
}

// Lchown changes the owner of name itself, which may be a symbolic link.
func (d *dir) Lchown(name string, uid, gid int) error {
	return d.changeTarget("lchownat", name, true, func(fd int) error {
		return unix.Fchownat(fd, "", uid, gid, unix.AT_EMPTY_PATH|unix.AT_SYMLINK_NOFOLLOW)
	})
}

// Chtimes uses utimensat with AT_EMPTY_PATH (Linux 5.8+), falling back to the
// /proc/self/fd magic link on EINVAL. A zero time is left unchanged.
func (d *dir) Chtimes(name string, atime, mtime time.Time) error {
	times := make([]unix.Timespec, 0, 2)
	for _, t := range []time.Time{atime, mtime} {
		times = append(times, kit.Ternary(t.IsZero(), unix.Timespec{Sec: unix.UTIME_OMIT, Nsec: unix.UTIME_OMIT}, unix.NsecToTimespec(t.UnixNano())))
	}
	return d.changeTarget("chtimesat", name, false, func(fd int) error {
		err := unix.UtimesNanoAt(fd, "", times, unix.AT_EMPTY_PATH)
		if errors.Is(err, unix.EINVAL) {
			return unix.UtimesNanoAt(unix.AT_FDCWD, "/proc/self/fd/"+strconv.Itoa(fd), times, 0)
		}
		return err
	})
}

func (d *dir) changeTarget(op, name string, allowSymlink bool, change func(fd int) error) error {
	fd, err := ignoringEINTR(func() (int, error) {
		return unix.Openat(d.fd, name, unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	})
	if err != nil {
		return &fs.PathError{Op: op, Path: d.errorPath(name), Err: err}
	}
	defer func() {
		_ = unix.Close(fd)
	}()

	var stat unix.Stat_t
	err = unix.Fstat(fd, &stat)
	if err != nil {
		return &fs.PathError{Op: op, Path: d.errorPath(name), Err: err}
	}
	if stat.Mode&unix.S_IFMT == unix.S_IFLNK && !allowSymlink {
		return fmt.Errorf("%w: %s", ErrSymlink, d.display(name))
	}
	err = change(fd)
	if err != nil {
		return &fs.PathError{Op: op, Path: d.errorPath(name), Err: err}
	}
	return nil
}

// syscallMode converts mode like the os package, keeping special bits.
func syscallMode(mode os.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&os.ModeSetuid != 0 {
		bits |= unix.S_ISUID
	}
	if mode&os.ModeSetgid != 0 {
		bits |= unix.S_ISGID
	}
	if mode&os.ModeSticky != 0 {
		bits |= unix.S_ISVTX
	}
	return bits
}

func ignoringEINTR[T any](call func() (T, error)) (T, error) {
	for {
		value, err := call()
		if !errors.Is(err, unix.EINTR) {
			return value, err
		}
	}
}
