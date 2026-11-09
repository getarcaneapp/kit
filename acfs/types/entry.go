// Package types contains the public data contracts used by ACFS and its
// command-line protocol.
package types

import "time"

type (
	// Entry describes a filesystem entry relative to a workspace root.
	Entry struct {
		ModTime         time.Time `json:"modTime"`
		Name            string    `json:"name"`
		Path            string    `json:"path"`
		Mode            string    `json:"mode"`
		LinkTarget      string    `json:"linkTarget,omitempty"`
		ModTimeUnixNano int64     `json:"modTimeUnixNano"`
		Size            int64     `json:"size"`
		// Device and Inode identify the file like os.SameFile; zero where the
		// platform has no Stat_t.
		Device uint64 `json:"device"`
		Inode  uint64 `json:"inode"`
		// UID and GID are the numeric owner; zero where the platform has no
		// Stat_t. They are never omitted, since 0 is root.
		UID int `json:"uid"`
		GID int `json:"gid"`
		// UnixMode carries the raw os.FileMode bits of the entry, so callers can
		// compare or reapply them. Mode is a human-readable rendering meant for
		// display only.
		UnixMode    uint32 `json:"unixMode"`
		IsDirectory bool   `json:"isDirectory"`
		IsSymlink   bool   `json:"isSymlink"`
	}

	// ListResponse is the versioned response emitted by the list command.
	ListResponse struct {
		Entries   []Entry `json:"entries"`
		Truncated bool    `json:"truncated,omitempty"`
		Version   int     `json:"version"`
	}

	// StatResponse is the versioned response emitted by the stat command.
	StatResponse struct {
		Entry   Entry `json:"entry"`
		Version int   `json:"version"`
	}

	// WalkRecord is one versioned record emitted by the walk command.
	WalkRecord struct {
		Entry     *Entry `json:"entry,omitempty"`
		End       bool   `json:"end,omitempty"`
		Truncated bool   `json:"truncated,omitempty"`
		Count     int    `json:"count,omitempty"`
		Version   int    `json:"version"`
	}

	// StatOptions configures Root.Stat. The zero value describes a final symbolic
	// link itself rather than its target.
	StatOptions struct {
		// FollowFinal resolves a final symbolic link; the returned Entry.Path is
		// then the fully resolved logical path, so Stat doubles as a root-confined
		// symlink resolver.
		FollowFinal bool
	}

	// WalkOptions bounds a recursive traversal. Zero values are unlimited.
	WalkOptions struct {
		MaxDepth   int
		MaxEntries int
	}

	// WalkResult summarizes a bounded recursive traversal.
	WalkResult struct {
		Count     int
		Truncated bool
	}

	// VersionResponse describes an acfs build and its supported protocol.
	VersionResponse struct {
		Version   string `json:"version"`
		Revision  string `json:"revision"`
		BuildTime string `json:"buildTime"`
		Protocol  int    `json:"protocol"`
	}
)

const (
	// ProtocolVersion is the current acfs wire protocol version.
	ProtocolVersion = 2
)
