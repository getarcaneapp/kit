// Package stats stores compact container stats history samples.
package stats

import (
	"math"
	"sync"
	"time"

	dockercontainer "github.com/moby/moby/api/types/container"
	kit "go.getarcane.app/kit/pkg"
)

const (
	DefaultHistoryCapacity = 30
	defaultHistoryTTL      = 10 * time.Minute
	defaultPruneInterval   = time.Minute
)

// StatsHistorySample is a compact history sample for container CPU and memory
// usage.
type StatsHistorySample struct {
	CPUTenths        uint16 `json:"cpuTenths"`
	MemoryTenths     uint16 `json:"memoryTenths"`
	MemoryUsageBytes uint64 `json:"memoryUsageBytes"`
}

// StatsStreamPayload is a container stats stream payload.
type StatsStreamPayload struct {
	dockercontainer.StatsResponse

	StatsHistory         []StatsHistorySample `json:"statsHistory,omitempty"`
	CurrentHistorySample StatsHistorySample   `json:"currentHistorySample"`
}

// Option configures a Store.
type Option func(*Store)

// WithCapacity sets the number of samples retained per container.
func WithCapacity(capacity int) Option {
	return func(s *Store) {
		if capacity > 0 {
			s.capacity = capacity
		}
	}
}

// WithTTL sets how long inactive container histories are retained.
func WithTTL(ttl time.Duration) Option {
	return func(s *Store) {
		if ttl > 0 {
			s.ttl = ttl
		}
	}
}

// WithPruneInterval sets the minimum time between automatic pruning passes.
func WithPruneInterval(interval time.Duration) Option {
	return func(s *Store) {
		if interval > 0 {
			s.pruneInterval = interval
		}
	}
}

// Store keeps bounded per-container stats history.
type Store struct {
	mu            sync.Mutex
	histories     map[string]*historyBuffer
	lastPrune     time.Time
	capacity      int
	ttl           time.Duration
	pruneInterval time.Duration
}

type historyBuffer struct {
	samples   []StatsHistorySample
	start     int
	count     int
	updatedAt time.Time
}

// NewStore creates a configured Store.
func NewStore(opts ...Option) *Store {
	s := &Store{}
	s.initLocked()
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// BuildSample converts a Docker stats response into Arcane's compact sample
// shape. CPU percentage intentionally uses cpuDelta/systemDelta without Docker
// CLI's OnlineCPUs multiplier, preserving Arcane's historical 0-100 normalized
// chart scale.
func BuildSample(stats dockercontainer.StatsResponse) StatsHistorySample {
	return StatsHistorySample{
		CPUTenths: uint16(math.Round(func() float64 {
			cpuDelta := float64(stats.CPUStats.CPUUsage.TotalUsage) - float64(stats.PreCPUStats.CPUUsage.TotalUsage)
			systemDelta := float64(stats.CPUStats.SystemUsage) - float64(stats.PreCPUStats.SystemUsage)
			if systemDelta <= 0 || cpuDelta <= 0 {
				return 0
			}
			return min(max((cpuDelta/systemDelta)*100, 0), 100)
		}() * 10)),
		MemoryTenths: uint16(math.Round(func() float64 {
			limit := stats.MemoryStats.Limit
			if limit == 0 {
				return 0
			}
			usage := calculateMemoryUsage(stats)
			return min(max((float64(usage)/float64(limit))*100, 0), 100)
		}() * 10)),
		MemoryUsageBytes: calculateMemoryUsage(stats),
	}
}

// Record appends sample and optionally returns the current history.
func (s *Store) Record(containerID string, sample StatsHistorySample, includeHistory bool, recordedAt time.Time) []StatsHistorySample {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	if s.lastPrune.IsZero() || kit.Ternary(recordedAt.IsZero(), time.Now(), recordedAt).Sub(s.lastPrune) >= s.pruneInterval {
		s.pruneLocked(recordedAt)
	}

	buffer := s.histories[containerID]
	if buffer == nil {
		buffer = &historyBuffer{samples: make([]StatsHistorySample, s.capacity)}
		s.histories[containerID] = buffer
	}

	buffer.append(sample, recordedAt)

	if !includeHistory {
		return nil
	}

	return buffer.snapshot()
}

// History returns the recorded history for containerID without recording a new
// sample.
func (s *Store) History(containerID string) []StatsHistorySample {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	buffer := s.histories[containerID]
	if buffer == nil {
		return nil
	}
	return buffer.snapshot()
}

// Remove evicts all history for containerID.
func (s *Store) Remove(containerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	delete(s.histories, containerID)
}

// Prune evicts expired histories.
func (s *Store) Prune(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	s.pruneLocked(now)
}

func (s *Store) initLocked() {
	// keep: lazy defaults under s.mu so a zero-value Store works
	if s.capacity <= 0 {
		s.capacity = DefaultHistoryCapacity
	}
	if s.ttl <= 0 {
		s.ttl = defaultHistoryTTL
	}
	if s.pruneInterval <= 0 {
		s.pruneInterval = defaultPruneInterval
	}
	if s.histories == nil {
		s.histories = make(map[string]*historyBuffer)
	}
}

func (b *historyBuffer) append(sample StatsHistorySample, recordedAt time.Time) {
	if b.count < len(b.samples) {
		index := (b.start + b.count) % len(b.samples)
		b.samples[index] = sample
		b.count++
	} else {
		b.samples[b.start] = sample
		b.start = (b.start + 1) % len(b.samples)
	}

	b.updatedAt = recordedAt
}

func (b *historyBuffer) snapshot() []StatsHistorySample {
	if b.count == 0 {
		return nil
	}

	out := make([]StatsHistorySample, 0, b.count)
	for i := range b.count {
		index := (b.start + i) % len(b.samples)
		out = append(out, b.samples[index])
	}
	return out
}

func (s *Store) pruneLocked(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	s.lastPrune = now
	for containerID, buffer := range s.histories {
		if buffer == nil || now.Sub(buffer.updatedAt) > s.ttl {
			delete(s.histories, containerID)
		}
	}
}

func calculateMemoryUsage(stats dockercontainer.StatsResponse) uint64 {
	usage := stats.MemoryStats.Usage
	if totalInactiveFile, ok := stats.MemoryStats.Stats["total_inactive_file"]; ok && totalInactiveFile < usage {
		return usage - totalInactiveFile
	}
	if inactiveFile, ok := stats.MemoryStats.Stats["inactive_file"]; ok && inactiveFile < usage {
		return usage - inactiveFile
	}

	return usage
}
