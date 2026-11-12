package tripwire

import "github.com/fsnotify/fsnotify"

type (
	// Op is a bitmask of filesystem operations.
	Op = fsnotify.Op

	// Event is one filesystem change under one of the watched roots.
	Event struct {
		// Path is the real path reported by the OS. Inside a followed symlinked
		// directory this is the path under the symlink target.
		Path string
		// Logical is Path as seen from its root, with followed symlinked
		// directories mapped back to the link's location.
		Logical string
		// Root is the cleaned entry of [Config.Roots] that Logical lies under.
		Root string
		// Rel is Logical relative to Root, using the OS path separator. It is
		// "." for the root itself.
		Rel string
		// Op is the set of operations in this event. Entries discovered inside a
		// newly created directory are reported as [Create].
		Op Op
	}

	// Batch is one debounced delivery to [Config.OnBatch].
	Batch[K comparable] struct {
		// Keys holds the distinct keys collected since the last batch, in no
		// particular order.
		Keys []K
		// Resync reports that events may have been lost, so the consumer should
		// reconcile everything instead of trusting Keys alone.
		Resync bool
	}
)

const (
	// Filesystem operations an [Event] can carry.
	Create = fsnotify.Create
	Write  = fsnotify.Write
	Remove = fsnotify.Remove
	Rename = fsnotify.Rename
	Chmod  = fsnotify.Chmod
)

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
