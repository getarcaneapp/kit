package tripwire

import "github.com/fsnotify/fsnotify"

// Op is a bitmask of filesystem operations.
type Op = fsnotify.Op

// Filesystem operations an [Event] can carry.
const (
	Create = fsnotify.Create
	Write  = fsnotify.Write
	Remove = fsnotify.Remove
	Rename = fsnotify.Rename
	Chmod  = fsnotify.Chmod
)

// Event is one filesystem change under the watched root.
type Event struct {
	// Path is the real path reported by the OS. Inside a followed symlinked
	// directory this is the path under the symlink target.
	Path string
	// Logical is Path as seen from the root, with followed symlinked
	// directories mapped back to the link's location.
	Logical string
	// Rel is Logical relative to the root, using the OS path separator.
	// It is "." for the root itself.
	Rel string
	// Op is the set of operations in this event. Entries discovered inside a
	// newly created directory are reported as [Create].
	Op Op
}

// Batch is one debounced delivery to [Config.OnBatch].
type Batch[K comparable] struct {
	// Keys holds the distinct keys collected since the last batch, in no
	// particular order.
	Keys []K
	// Resync reports that events may have been lost, so the consumer should
	// reconcile everything instead of trusting Keys alone.
	Resync bool
}

// PathOf is a [Config.Key] for Watcher[string] that keys every event by its
// real path.
func PathOf(e Event) (string, bool) { return e.Path, true }

// Signal is a [Config.Key] that maps every event to K's zero value, so a burst
// of changes becomes a single key. Use it as Key: tripwire.Signal on a
// Watcher[struct{}]; the type argument is inferred from the field.
func Signal[K comparable](Event) (K, bool) {
	var zero K
	return zero, true
}
