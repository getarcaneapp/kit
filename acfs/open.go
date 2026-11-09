package acfs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"kit.getarcane.app"
	kitfs "kit.getarcane.app/fs"
)

// OpenOptions selects how OpenFile opens a regular file. Read or Write is required.
type OpenOptions struct {
	// Mode is the permission set of a file created by Create.
	Mode   os.FileMode
	Read   bool
	Write  bool
	Create bool
	// Exclusive, which requires Create, reports ErrAlreadyExists for an existing file.
	Exclusive bool
	// Truncate, which requires Write, empties an existing file.
	Truncate bool
	// Append, which requires Write, makes every write append to the file.
	Append bool
}

func (o OpenOptions) flags() (int, error) {
	if o.Mode&^chmodModeMask != 0 {
		return 0, fmt.Errorf("%w: invalid file mode %v", ErrInvalidOpenOptions, o.Mode)
	}
	if !o.Read && !o.Write {
		return 0, fmt.Errorf("%w: Read or Write is required", ErrInvalidOpenOptions)
	}
	if (o.Truncate || o.Append) && !o.Write {
		return 0, fmt.Errorf("%w: Truncate and Append require Write", ErrInvalidOpenOptions)
	}
	if o.Exclusive && !o.Create {
		return 0, fmt.Errorf("%w: Exclusive requires Create", ErrInvalidOpenOptions)
	}
	return kit.Ternary(o.Read && o.Write, os.O_RDWR, os.O_RDONLY) |
		kit.Ternary(o.Write && !o.Read, os.O_WRONLY, 0) |
		kit.Ternary(o.Create, os.O_CREATE, 0) |
		kit.Ternary(o.Exclusive, os.O_EXCL, 0) |
		kit.Ternary(o.Truncate, os.O_TRUNC, 0) |
		kit.Ternary(o.Append, os.O_APPEND, 0), nil
}

// OpenFile opens a root-confined regular file. Writes and creates refuse symbolic
// links with ErrSymlink; read-only opens follow links inside the root.
func (r Root) OpenFile(ctx context.Context, logicalPath string, options OpenOptions) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	flags, err := options.flags()
	if err != nil {
		return nil, err
	}
	relativePath, err := kitfs.NormalizeLogicalPath(logicalPath)
	if err != nil {
		return nil, err
	}
	err = rejectReservedPath(relativePath)
	if err != nil {
		return nil, err
	}
	if relativePath == "." {
		return nil, ErrIsDirectory
	}

	if options.Write || options.Create {
		return r.openFileForMutation(relativePath, logicalPath, flags, options)
	}

	root, err := os.OpenRoot(r.Path)
	if err != nil {
		return nil, fmt.Errorf("open workspace root: %w", err)
	}
	defer func() {
		_ = root.Close()
	}()

	name, info, err := resolvePath(root, relativePath, resolveOptions{FollowFinal: true})
	if err == nil && info == nil {
		info, err = root.Stat(name)
	}
	err = checkOpenTarget(info, err, options, logicalPath)
	if err != nil {
		return nil, err
	}
	file, err := root.OpenFile(name, flags|syscall.O_NONBLOCK, options.Mode)
	if err != nil {
		return nil, openError(err, logicalPath)
	}
	err = verifyOpenedFile(file)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// openFileForMutation opens the target without following links and applies
// O_TRUNC only once the descriptor is known to be a regular file.
func (r Root) openFileForMutation(relativePath, logicalPath string, flags int, options OpenOptions) (*os.File, error) {
	mutation, err := openMutationRoot(r.Path)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = mutation.Close()
	}()
	parent, base, err := mutation.openParent(relativePath, false)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = parent.Close()
	}()

	info, err := parent.Lstat(base)
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%w: %s", ErrSymlink, logicalPath)
	}
	err = checkOpenTarget(info, err, options, logicalPath)
	if err != nil {
		return nil, err
	}

	file, err := parent.OpenFile(base, flags&^os.O_TRUNC, options.Mode)
	if err != nil {
		return nil, openError(err, logicalPath)
	}
	err = verifyOpenedFile(file)
	if err != nil {
		return nil, err
	}
	if options.Truncate {
		err = file.Truncate(0)
		if err != nil {
			_ = file.Close()
			return nil, fmt.Errorf("truncate %q: %w", logicalPath, err)
		}
	}
	return file, nil
}

// OpenFile opens a host regular file, following symbolic links.
func (Host) OpenFile(ctx context.Context, hostPath string, options OpenOptions) (*os.File, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	flags, err := options.flags()
	if err != nil {
		return nil, err
	}
	filePath, err := cleanHostFilePath(hostPath)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(filePath)
	err = checkOpenTarget(info, err, options, filePath)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filePath, flags|syscall.O_NONBLOCK, options.Mode)
	if err != nil {
		return nil, openError(err, filePath)
	}
	err = verifyOpenedFile(file)
	if err != nil {
		return nil, err
	}
	return file, nil
}

// checkOpenTarget refuses directories and FIFOs before an open could block on them.
func checkOpenTarget(info os.FileInfo, statErr error, options OpenOptions, displayPath string) error {
	if errors.Is(statErr, fs.ErrNotExist) && options.Create {
		return nil
	}
	if statErr != nil {
		return fmt.Errorf("open %q: %w", displayPath, statErr)
	}
	if options.Exclusive {
		return fmt.Errorf("%w: %q: %w", ErrAlreadyExists, displayPath, fs.ErrExist)
	}
	return requireRegular(info)
}

func openError(err error, displayPath string) error {
	if errors.Is(err, syscall.ENXIO) {
		return fmt.Errorf("%w: %q", ErrNotFile, displayPath)
	}
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w: %q: %w", ErrAlreadyExists, displayPath, fs.ErrExist)
	}
	return fmt.Errorf("open %q: %w", displayPath, err)
}

// verifyOpenedFile closes file unless it is a regular file.
func verifyOpenedFile(file *os.File) error {
	opened, err := file.Stat()
	if err == nil {
		err = requireRegular(opened)
	}
	if err != nil {
		_ = file.Close()
		return err
	}
	return nil
}
