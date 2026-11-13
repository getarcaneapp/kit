package acfs

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"

	kitfs "kit.getarcane.app/fs"
)

type (
	// WriteOptions configures a write.
	WriteOptions struct {
		Mode os.FileMode
		// InPlace keeps existing inodes and refuses a final symbolic link, but is not atomic.
		InPlace bool
		// Exclusive publishes the file only if the target does not exist, reporting
		// ErrAlreadyExists otherwise. It cannot be combined with InPlace.
		Exclusive bool
	}

	// WriteFromOptions configures WriteFrom.
	WriteFromOptions struct {
		// Size is the exact number of bytes to read from the source.
		Size int64
		// Mode is the permission set of the written file.
		Mode os.FileMode
		// Exclusive publishes the file only if the target does not exist, reporting
		// ErrAlreadyExists otherwise.
		Exclusive bool
	}

	contextReader struct {
		ctx    context.Context
		reader io.Reader
	}
)

const (
	temporaryWritePrefix = ".acfs-write-"
	chmodModeMask        = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
)

func (o WriteOptions) validate() error {
	if o.Mode&^chmodModeMask != 0 {
		return fmt.Errorf("invalid file mode %v", o.Mode)
	}
	if o.Exclusive && o.InPlace {
		return fmt.Errorf("%w: Exclusive cannot be combined with InPlace", ErrInvalidWriteOptions)
	}
	return nil
}

// WriteFrom atomically writes exactly options.Size bytes from source to a
// root-confined file.
func (r Root) WriteFrom(ctx context.Context, logicalPath string, source io.Reader, options WriteFromOptions) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if source == nil {
		return 0, errors.New("source reader is required")
	}
	if options.Size < 0 {
		return 0, fmt.Errorf("%w: size must be non-negative", ErrSizeMismatch)
	}
	if options.Mode&^chmodModeMask != 0 {
		return 0, fmt.Errorf("invalid file options.Mode %v", options.Mode)
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return 0, err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return 0, err
	}
	if relativePath == "." {
		return 0, ErrIsDirectory
	}

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = mutation.Close()
	}()

	parent, base, err := mutation.openResolvedParent(relativePath, func(target string) error {
		return fmt.Errorf("create temporary file: %w", &fs.PathError{Op: "openat", Path: path.Join(path.Dir(target), temporaryWriteName()), Err: syscall.ENOTDIR})
	})
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = parent.Close()
	}()
	return writeFromRoot(ctx, parent, logicalPath, base, source, options)
}

// Write atomically replaces a root-confined file with data, or rewrites it in
// place with options.InPlace.
func (r Root) Write(ctx context.Context, logicalPath string, data []byte, options WriteOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := options.validate(); err != nil {
		return err
	}
	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return ErrIsDirectory
	}
	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()
	parent, base, err := mutation.openResolvedParent(relativePath, func(target string) error {
		if options.InPlace {
			return fmt.Errorf("inspect %q: %w", kitfs.LogicalPath(target), &fs.PathError{Op: "statat", Path: target, Err: syscall.ENOTDIR})
		}
		return fmt.Errorf("create temporary file: %w", &fs.PathError{Op: "openat", Path: path.Join(path.Dir(target), temporaryWriteName()), Err: syscall.ENOTDIR})
	})
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()
	err = rejectReservedPath(path.Join(parent.path, base))
	if err != nil {
		return err
	}
	if !options.InPlace {
		_, err = writeFromRoot(ctx, parent, logicalPath, base, bytes.NewReader(data), WriteFromOptions{
			Size:      int64(len(data)),
			Mode:      options.Mode,
			Exclusive: options.Exclusive,
		})
		return err
	}
	return writeInPlace(ctx, parent, base, data, options.Mode)
}

// writeInPlace rewrites targetPath in files, keeping its inode.
func writeInPlace(ctx context.Context, files *dir, targetPath string, data []byte, mode os.FileMode) (retErr error) {
	info, err := files.Lstat(targetPath)
	flags := os.O_WRONLY
	identical := false
	switch {
	case errors.Is(err, os.ErrNotExist):
		flags |= os.O_CREATE | os.O_EXCL
	case err != nil:
		return fmt.Errorf("inspect %q: %w", files.display(targetPath), err)
	case info.Mode()&os.ModeSymlink != 0:
		return ErrSymlink
	case info.IsDir():
		return ErrIsDirectory
	case !info.Mode().IsRegular():
		return ErrNotFile
	case info.Size() == int64(len(data)):
		existing, openErr := files.OpenFile(targetPath, os.O_RDONLY, 0)
		if openErr != nil {
			return fmt.Errorf("read %q: %w", files.display(targetPath), openErr)
		}
		identical, err = equalContent(existing, bytes.NewReader(data), make([]byte, 2*compareChunkSize))
		_ = existing.Close()
		if err != nil {
			return fmt.Errorf("read %q: %w", files.display(targetPath), err)
		}
		if identical && info.Mode()&chmodModeMask == mode {
			return ctx.Err()
		}
	}

	file, err := files.OpenFile(targetPath, flags, mode)
	if err != nil {
		return fmt.Errorf("open %q: %w", files.display(targetPath), err)
	}
	defer func() {
		retErr = errors.Join(retErr, file.Close())
	}()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat opened file: %w", err)
	}
	live, err := files.Lstat(targetPath)
	if err != nil {
		return fmt.Errorf("stat destination: %w", err)
	}
	if live.Mode()&os.ModeSymlink != 0 {
		return ErrSymlink
	}
	if !opened.Mode().IsRegular() || !live.Mode().IsRegular() || !os.SameFile(opened, live) || (info != nil && !os.SameFile(info, opened)) {
		return fmt.Errorf("%w: destination changed before writing", ErrNotFile)
	}
	err = ctx.Err()
	if err != nil {
		return err
	}
	if !identical {
		if opened.Size() > 0 {
			err = file.Truncate(0)
			if err != nil {
				return fmt.Errorf("truncate %q: %w", files.display(targetPath), err)
			}
		}
		_, err = file.Write(data)
		if err != nil {
			return fmt.Errorf("write %q: %w", files.display(targetPath), err)
		}
	}
	if !identical || opened.Mode()&chmodModeMask != mode {
		err = file.Chmod(mode)
		if err != nil {
			return fmt.Errorf("chmod %q: %w", files.display(targetPath), err)
		}
	}
	return nil
}

// WriteAt writes data at offset into an existing root-confined regular file,
// in place.
func (r Root) WriteAt(ctx context.Context, logicalPath string, offset int64, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if offset < 0 {
		return fmt.Errorf("%w: offset must be non-negative", ErrInvalidPath)
	}

	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return err
	}
	if relativePath == "." {
		return ErrIsDirectory
	}

	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return err
	}
	defer func() {
		_ = mutation.Close()
	}()

	root, err := mutation.workspace()
	if err != nil {
		return err
	}
	resolvedPath, _, err := resolvePath(root, relativePath, resolveOptions{FollowFinal: true})
	if err != nil {
		return err
	}
	parent, err := mutation.top.openDir(path.Dir(resolvedPath), dirOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = parent.Close()
	}()

	file, err := parent.OpenFile(path.Base(resolvedPath), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("open %q: %w", kitfs.LogicalPath(resolvedPath), err)
	}
	return writeAtAndClose(file, logicalPath, offset, data)
}

// writeAtAndClose writes data at offset into file and closes it.
func writeAtAndClose(file *os.File, displayPath string, offset int64, data []byte) error {
	defer func() {
		_ = file.Close()
	}()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", displayPath, err)
	}
	if !info.Mode().IsRegular() {
		return ErrNotFile
	}

	_, err = file.WriteAt(data, offset)
	if err != nil {
		return fmt.Errorf("write %q at offset %d: %w", displayPath, offset, err)
	}
	return nil
}

func rejectReservedPath(relativePath string) error {
	for component := range strings.SplitSeq(relativePath, "/") {
		if strings.HasPrefix(component, temporaryWritePrefix) {
			return fmt.Errorf("%w: %q uses reserved ACFS name", ErrInvalidPath, component)
		}
	}
	return nil
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

// temporaryWriteName returns a random reserved name for a staged write.
func temporaryWriteName() string {
	var randomBytes [16]byte
	_, _ = rand.Read(randomBytes[:])
	var filename [len(temporaryWritePrefix) + 2*len(randomBytes)]byte
	copy(filename[:], temporaryWritePrefix)
	hex.Encode(filename[len(temporaryWritePrefix):], randomBytes[:])
	return string(filename[:])
}

// createTemporaryFile creates a uniquely named temporary file in files.
func createTemporaryFile(files *dir) (*os.File, string, error) {
	for range 10 {
		name := temporaryWriteName()
		file, err := files.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return file, name, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, "", fmt.Errorf("create temporary file: %w", err)
		}
	}
	return nil, "", errors.New("could not allocate a unique temporary file")
}

// writeFromRoot stages options.Size bytes from source in files and renames the
// staged file onto targetName, reporting ErrReplaced if either name is swapped.
// With options.Exclusive an existing targetName is never replaced.
func writeFromRoot(ctx context.Context, files *dir, displayPath, targetName string, source io.Reader, options WriteFromOptions) (int64, error) {
	if options.Exclusive {
		_, err := files.Lstat(targetName)
		if err == nil {
			return 0, fmt.Errorf("%w: %q: %w", ErrAlreadyExists, displayPath, fs.ErrExist)
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return 0, fmt.Errorf("inspect %q: %w", displayPath, err)
		}
	}
	temporaryFile, temporaryName, err := createTemporaryFile(files)
	if err != nil {
		return 0, err
	}
	committed := false
	defer func() {
		_ = temporaryFile.Close()
		if !committed {
			_ = files.Remove(temporaryName)
		}
	}()

	reader := contextReader{ctx: ctx, reader: source}
	written, err := io.CopyN(temporaryFile, reader, options.Size)
	if err != nil {
		return written, fmt.Errorf("%w: expected %d bytes, received %d: %w", ErrSizeMismatch, options.Size, written, err)
	}

	var extra [1]byte
	extraCount, extraErr := io.ReadFull(reader, extra[:])
	if extraCount != 0 {
		return written, fmt.Errorf("%w: input exceeds %d bytes", ErrSizeMismatch, options.Size)
	}
	if extraErr != nil && !errors.Is(extraErr, io.EOF) {
		return written, fmt.Errorf("check input size: %w", extraErr)
	}

	err = temporaryFile.Chmod(options.Mode)
	if err != nil {
		return written, fmt.Errorf("chmod temporary file: %w", err)
	}
	err = temporaryFile.Sync()
	if err != nil {
		return written, fmt.Errorf("sync temporary file: %w", err)
	}
	if beforePublish != nil {
		beforePublish(temporaryName)
	}
	err = files.verifyPublished(temporaryName, temporaryFile)
	if err != nil {
		committed = true
		return written, err
	}
	if options.Exclusive {
		err = files.renameNoReplace(temporaryName, files, targetName)
		if errors.Is(err, fs.ErrExist) {
			return written, fmt.Errorf("%w: %q: %w", ErrAlreadyExists, displayPath, fs.ErrExist)
		}
	} else {
		err = files.renameTo(temporaryName, files, targetName)
	}
	if err != nil {
		return written, fmt.Errorf("replace %q: %w", displayPath, err)
	}
	committed = true
	if afterPublish != nil {
		afterPublish()
	}
	err = files.verifyPublished(targetName, temporaryFile)
	if err != nil {
		return written, err
	}
	err = temporaryFile.Close()
	if err != nil {
		return written, fmt.Errorf("close temporary file: %w", err)
	}
	return written, nil
}
