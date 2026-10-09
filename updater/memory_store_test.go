package updater

import (
	"context"
	"errors"
	"testing"
)

func TestMemoryPendingStoreReadsAndClearsRecords(t *testing.T) {
	store := NewMemoryPendingStore(
		ImageUpdateRecord{ID: "b", Repository: "redis", Tag: "7", HasUpdate: true},
		ImageUpdateRecord{ID: "a", Repository: "nginx", Tag: "1.27", HasUpdate: true},
	)

	records, err := store.PendingImageUpdates(t.Context())
	if err != nil {
		t.Fatalf("PendingImageUpdates() error = %v", err)
	}
	if len(records) != 2 || records[0].ID != "a" || records[1].ID != "b" {
		t.Fatalf("PendingImageUpdates() = %#v, want records sorted by key", records)
	}

	if err = store.ClearImageUpdateRecord(t.Context(), records[0]); err != nil {
		t.Fatalf("ClearImageUpdateRecord() error = %v", err)
	}
	records, err = store.PendingImageUpdates(t.Context())
	if err != nil {
		t.Fatalf("PendingImageUpdates() after clear error = %v", err)
	}
	if len(records) != 1 || records[0].ID != "b" {
		t.Fatalf("PendingImageUpdates() after clear = %#v, want only b", records)
	}
}

func TestMemoryPendingStoreRespectsCanceledContext(t *testing.T) {
	store := NewMemoryPendingStore(ImageUpdateRecord{ID: "a", Repository: "repo", Tag: "1"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := store.PendingImageUpdates(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("PendingImageUpdates() error = %v, want context canceled", err)
	}
	if err := store.ClearImageUpdateRecord(ctx, ImageUpdateRecord{ID: "a"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ClearImageUpdateRecord() error = %v, want context canceled", err)
	}

	records, err := store.PendingImageUpdates(t.Context())
	if err != nil {
		t.Fatalf("PendingImageUpdates() after canceled clear error = %v", err)
	}
	if len(records) != 1 || records[0].ID != "a" {
		t.Fatalf("PendingImageUpdates() after canceled clear = %#v, want record a", records)
	}
}

func TestMemoryPendingStoreScopesSharedImage(t *testing.T) {
	latest := "1.1.0"
	first := ImageUpdateRecord{ID: "shared", ContainerID: "first", Repository: "app", Tag: "1.0.0", LatestVersion: &latest, HasUpdate: true, UpdateType: UpdateTypeTag}
	second := first
	second.ContainerID = "second"
	store := NewMemoryPendingStore(first, second)
	if err := store.ClearImageUpdateRecord(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	records, err := store.PendingImageUpdates(t.Context())
	if err != nil || len(records) != 1 || records[0].ContainerID != "second" {
		t.Fatalf("records %+v, err %v", records, err)
	}
}
