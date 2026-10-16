package updater

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"strconv"
	"sync"
)

type memoryPendingStore struct {
	mu      sync.Mutex
	records map[string]ImageUpdateRecord
}

// NewMemoryPendingStore returns an in-memory pending update store.
func NewMemoryPendingStore(records ...ImageUpdateRecord) PendingStore {
	byKey := make(map[string]ImageUpdateRecord, len(records))
	for _, record := range records {
		byKey[strconv.Quote(record.ContainerID)+":"+strconv.Quote(cmp.Or(record.ID, record.ImageRef()))] = record
	}
	return &memoryPendingStore{records: byKey}
}

func (s *memoryPendingStore) PendingImageUpdates(ctx context.Context) ([]ImageUpdateRecord, error) {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]ImageUpdateRecord, 0, len(s.records))
	for _, key := range slices.Sorted(maps.Keys(s.records)) {
		out = append(out, s.records[key])
	}
	return out, nil
}

func (s *memoryPendingStore) ClearImageUpdateRecord(ctx context.Context, record ImageUpdateRecord) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.records, strconv.Quote(record.ContainerID)+":"+strconv.Quote(cmp.Or(record.ID, record.ImageRef())))
	return nil
}
