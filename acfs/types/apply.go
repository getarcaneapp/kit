package types

import "fmt"

// ErrorCode is a stable machine-readable ACFS failure category.
type ErrorCode string

const (
	ErrorInvalidPath  ErrorCode = "invalid_path"
	ErrorOutsideRoot  ErrorCode = "outside_root"
	ErrorSymlink      ErrorCode = "symlink"
	ErrorSymlinkLoop  ErrorCode = "symlink_loop"
	ErrorNotFound     ErrorCode = "not_found"
	ErrorAlreadyExist ErrorCode = "already_exists"
	ErrorNotDirectory ErrorCode = "not_directory"
	ErrorNotFile      ErrorCode = "not_file"
	ErrorNotEmpty     ErrorCode = "not_empty"
	ErrorIsDirectory  ErrorCode = "is_directory"
	ErrorSizeMismatch ErrorCode = "size_mismatch"
	ErrorRootRemoval  ErrorCode = "root_removal"
	ErrorInternal     ErrorCode = "internal"
)

// ErrorResponse is emitted as one JSON line on stderr when a command fails.
type ErrorResponse struct {
	Code        ErrorCode `json:"code"`
	Message     string    `json:"message"`
	Operation   string    `json:"operation,omitempty"`
	Path        string    `json:"path,omitempty"`
	ChangeIndex *int      `json:"changeIndex,omitempty"`
	Version     int       `json:"version"`
}

// ApplyOperation identifies one ordered filesystem mutation.
type ApplyOperation string

const (
	ApplyCreateFile   ApplyOperation = "create_file"
	ApplyUpdateFile   ApplyOperation = "update_file"
	ApplyCreateFolder ApplyOperation = "create_folder"
	ApplyRename       ApplyOperation = "rename"
	ApplyMove         ApplyOperation = "move"
	ApplyDelete       ApplyOperation = "delete"
)

// ApplyChange describes one ordered root-confined filesystem mutation.
type ApplyChange struct {
	Operation  ApplyOperation `json:"operation"`
	Path       string         `json:"path"`
	TargetPath string         `json:"targetPath,omitempty"`
	StagedName string         `json:"stagedName,omitempty"`
	Size       int64          `json:"size,omitempty"`
	Recursive  bool           `json:"recursive,omitempty"`
}

// ApplyManifest is the versioned input consumed by acfs apply.
type ApplyManifest struct {
	Changes []ApplyChange `json:"changes"`
	Version int           `json:"version"`
}

// ApplyResponse confirms that every mutation in a manifest was applied.
type ApplyResponse struct {
	Applied int `json:"applied"`
	Version int `json:"version"`
}

// ApplyError identifies the manifest change that failed.
type ApplyError struct {
	Index     int
	Operation ApplyOperation
	Path      string
	Err       error
}

func (e *ApplyError) Error() string {
	return fmt.Sprintf("apply change %d (%s %q): %v", e.Index, e.Operation, e.Path, e.Err)
}

func (e *ApplyError) Unwrap() error {
	return e.Err
}
