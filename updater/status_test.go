package updater

import (
	"errors"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatusTracksContainers(t *testing.T) {
	service := newServiceForTest(t, Config{})
	done, err := service.BeginContainerUpdate("abc")
	if err != nil {
		t.Fatalf("BeginContainerUpdate() error = %v", err)
	}
	status := service.Status()
	if status.UpdatingContainers != 1 || len(status.ContainerIDs) != 1 || status.ContainerIDs[0] != "abc" {
		t.Fatalf("Status() while updating = %#v", status)
	}
	done()
	status = service.Status()
	if status.UpdatingContainers != 0 || len(status.ContainerIDs) != 0 {
		t.Fatalf("Status() after done = %#v", status)
	}
}

func TestBeginContainerUpdateClaimsEachContainerOnce(t *testing.T) {
	service := newServiceForTest(t, Config{})
	var claimed, attempted atomic.Int32
	var workers sync.WaitGroup
	release := make(chan struct{})
	for range 16 {
		workers.Go(func() {
			end, err := service.BeginContainerUpdate("shared")
			attempted.Add(1)
			if err != nil {
				if !errors.Is(err, ErrUpdateInProgress) {
					t.Errorf("BeginContainerUpdate() error = %v, want ErrUpdateInProgress", err)
				}
				return
			}
			claimed.Add(1)
			<-release
			end()
		})
	}
	for attempted.Load() < 16 {
		runtime.Gosched()
	}
	close(release)
	workers.Wait()
	if got := claimed.Load(); got != 1 {
		t.Fatalf("claims = %d, want exactly 1", got)
	}
	end, err := service.BeginContainerUpdate("shared")
	if err != nil {
		t.Fatalf("BeginContainerUpdate() after release error = %v", err)
	}
	end()
}

func TestStatusSnapshotsRemainConsistentUnderConcurrentUpdates(t *testing.T) {
	service := newServiceForTest(t, Config{})
	containerIDs := []string{"alpha", "beta", "gamma", "delta"}
	projectIDs := []string{"project-a", "project-b", "project-c"}

	stopPolling := make(chan struct{})
	errCh := make(chan string, 1)
	var poller sync.WaitGroup
	poller.Go(func() {
		for {
			select {
			case <-stopPolling:
				return
			default:
			}
			if message := statusConsistencyError(service.Status()); message != "" {
				select {
				case errCh <- message:
				default:
				}
				return
			}
		}
	})

	var workers sync.WaitGroup
	for workerID := range 16 {
		workers.Go(func() {
			for i := range 200 {
				// A claim lost to another worker hands back a no-op release.
				endContainer, _ := service.BeginContainerUpdate(containerIDs[(workerID+i)%len(containerIDs)])
				endProject := service.BeginProjectUpdate(projectIDs[(workerID+i)%len(projectIDs)])
				if message := statusConsistencyError(service.Status()); message != "" {
					select {
					case errCh <- message:
					default:
					}
					return
				}
				time.Sleep(time.Microsecond)
				endProject()
				endContainer()
			}
		})
	}
	workers.Wait()
	close(stopPolling)
	poller.Wait()

	select {
	case message := <-errCh:
		t.Fatal(message)
	default:
	}
}

func statusConsistencyError(status Status) string {
	if status.UpdatingContainers != len(status.ContainerIDs) {
		return "container status count does not match IDs"
	}
	if status.UpdatingProjects != len(status.ProjectIDs) {
		return "project status count does not match IDs"
	}
	if !slices.IsSorted(status.ContainerIDs) {
		return "container status IDs are not sorted"
	}
	if !slices.IsSorted(status.ProjectIDs) {
		return "project status IDs are not sorted"
	}
	if hasDuplicateSortedString(status.ContainerIDs) {
		return "container status IDs contain duplicates"
	}
	if hasDuplicateSortedString(status.ProjectIDs) {
		return "project status IDs contain duplicates"
	}
	return ""
}

func hasDuplicateSortedString(values []string) bool {
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return true
		}
	}
	return false
}
