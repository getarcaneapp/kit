package updater

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"kit.getarcane.app"
)

// Status returns a point-in-time updater status snapshot.
func (s *Service) Status() Status {
	containerIDs := append([]string{}, kit.FromPtr(s.updatingContainers.Load())...)
	projectIDs := append([]string{}, kit.FromPtr(s.updatingProjects.Load())...)
	return Status{
		UpdatingContainers: len(containerIDs),
		UpdatingProjects:   len(projectIDs),
		ContainerIDs:       containerIDs,
		ProjectIDs:         projectIDs,
	}
}

// BeginContainerUpdate claims a container for updating and returns the callback that releases it. It
// returns ErrUpdateInProgress, with a no-op callback, while another update holds the same container.
func (s *Service) BeginContainerUpdate(containerID string) (func(), error) {
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return func() {}, nil
	}
	if !updateStatusSnapshot(&s.updatingContainers, containerID, true) {
		return func() {}, fmt.Errorf("%w: %s", ErrUpdateInProgress, containerID)
	}
	return func() { updateStatusSnapshot(&s.updatingContainers, containerID, false) }, nil
}

// BeginProjectUpdate marks a project as updating and returns a completion callback.
func (s *Service) BeginProjectUpdate(projectID string) func() {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return func() {}
	}
	updateStatusSnapshot(&s.updatingProjects, projectID, true)
	return func() { updateStatusSnapshot(&s.updatingProjects, projectID, false) }
}

// updateStatusSnapshot adds or removes id in the sorted snapshot active points at, retrying lost CAS
// races, and reports whether it changed the snapshot.
func updateStatusSnapshot(active *atomic.Pointer[[]string], id string, add bool) bool {
	for {
		currentPtr := active.Load()
		current := kit.FromPtr(currentPtr)
		index, found := slices.BinarySearch(current, id)
		if found == add {
			return false
		}
		next := slices.Clone(current)
		if add {
			next = slices.Insert(next, index, id)
		} else {
			next = slices.Delete(next, index, index+1)
		}
		if active.CompareAndSwap(currentPtr, &next) {
			return true
		}
	}
}
