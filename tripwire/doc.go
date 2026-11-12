// Package tripwire watches one or more directory trees for changes and
// delivers debounced, deduplicated batches of caller-defined keys.
//
// A [Watcher] subscribes to every directory under each of [Config.Roots] (up
// to [Config.MaxDepth] levels below each root), attaches new directories as
// they appear, and maps each filesystem event to a key with [Config.Key].
// [Event.Root] and [Event.Rel] say which root an event belongs to. All roots
// share one subscription and one debounce window: keys collect in a single
// set until every root goes quiet for [Config.Debounce], or until
// [Config.MaxDelay] passes under constant churn. Then they are handed to
// [Config.OnBatch] in a single [Batch].
//
// The key type picks the batching behaviour:
//
//   - Watcher[string] with [PathOf] reports which paths changed.
//   - Watcher[struct{}] with [Signal] collapses any burst into one call.
//
// When events may have been lost (inotify queue overflow, a lost
// subscription, or a root being removed, renamed, or restored), the next
// batch has [Batch.Resync] set and the consumer should do a full reconcile.
// Losing one root does not interrupt the others; it is retried with
// [Config.Backoff] until it comes back.
//
// tripwire uses os.* rather than acfs on purpose: it watches arbitrary
// user-configured paths and, with [Config.FollowSymlinks], follows symlinked
// directories whose targets may live outside any confinement root.
package tripwire
