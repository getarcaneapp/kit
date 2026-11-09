// Package acfs provides filesystem operations for Arcane, either confined to a
// root directory (Root) or on absolute host paths (Host).
//
// Root never reads, writes, or follows a link outside its Path. Some mutations
// resolve links inside the root in the parent path and others refuse them; the
// README lists which. On Linux mutations act on a parent opened by descriptor
// without following links; other systems check, then act by path.
// A write that finds its staged or published file replaced reports ErrReplaced.
//
// Operating system errors are wrapped, so errors.Is with fs.ErrNotExist,
// fs.ErrExist, and os.ErrPermission keeps working. RemoveAll ignores a missing
// target; Remove reports it.
//
// Writes are atomic by default and stage a temporary file with the reserved
// ".acfs-write-" prefix, which paths may not use and listings hide.
package acfs
