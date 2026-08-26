// Package acfs provides root-confined filesystem operations for Arcane
// helper processes.
//
// Every operation takes a host rootPath and an absolute logical path
// interpreted relative to it, and can never touch anything outside that root:
// ".." components are collapsed, and a symbolic link whose target escapes the
// root reports ErrOutsideRoot rather than being followed.
//
// # Errors
//
// The sentinels in errors.go classify ACFS-level policy failures. Errors that
// originate in the operating system are wrapped rather than translated, so the
// standard library predicates keep working and are part of the API contract:
// errors.Is(err, fs.ErrNotExist), errors.Is(err, fs.ErrExist), and
// errors.Is(err, os.ErrPermission) all hold through ACFS wrapping. That is why
// there is no ErrNotExist sentinel of our own.
//
// Removal deliberately has two behaviours. RemoveAll returns nil when the
// target is already gone, matching os.RemoveAll. Remove surfaces the not-exist
// error instead, so bounded cleanup loops (prune empty parents until something
// stops you) can terminate on any error without a separate existence probe.
//
// # Writes
//
// Writes are atomic by default: WriteFile and WriteFrom stage the content in a
// temporary file inside the target's directory and rename it into place, so a
// reader never observes a torn file. The one exception is MirrorDir, which
// rewrites destination files in place to preserve their inodes for live bind
// mounts; its documentation explains why.
//
// Temporary files created during a write use the reserved ".acfs-write-"
// name prefix. Paths containing a component with that prefix are rejected, and
// List, Walk, and CopyDir hide them.
//
// # Subpackages
//
// Subpackage atomic provides an unrooted atomic write for the handful of
// destinations that have no natural workspace root, such as a user-specified
// output file. It refuses a symlinked destination instead of replacing it.
package acfs
