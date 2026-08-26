package types

// CopyOptions configures a directory copy.
type CopyOptions struct {
	// TolerateUnreadable skips files and whole subdirectories that cannot be
	// read because of a permission error, or that vanished mid-walk, instead
	// of aborting the copy. Every other error still aborts.
	TolerateUnreadable bool
}

// CopyResult summarizes a completed directory copy.
type CopyResult struct {
	// Skipped lists the source-relative paths that were not copied, sorted.
	// It always includes sockets, FIFOs, and device nodes, which have no
	// copyable content, and additionally includes unreadable entries when
	// CopyOptions.TolerateUnreadable is set.
	Skipped []string
	// Copied counts the regular files written to the destination.
	Copied int
}

// MirrorOptions configures a directory mirror.
type MirrorOptions struct {
	// Preserve lists destination-relative paths that must survive the mirror
	// even though the source omits them, along with any parent directory that
	// still contains one. It is how a restore keeps files the backup could not
	// read.
	Preserve []string
}
