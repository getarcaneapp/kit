package concurrency

import (
	"errors"
	"testing"
)

func TestStateMapSerializesMutationsAndPublishesSnapshots(t *testing.T) {
	state := NewStateMap[string, int]()

	if err := state.Apply(t.Context(), "store value", func(values map[string]int) (bool, error) {
		values["answer"] = 42
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}
	if value, ok := state.Get("answer"); !ok || value != 42 {
		t.Fatalf("Get(answer) = (%d, %t), want (42, true)", value, ok)
	}
}

func TestStateMapPublishesPartialMutationBeforeReturningError(t *testing.T) {
	state := NewStateMap[string, int]()
	if err := state.Apply(t.Context(), "seed", func(values map[string]int) (bool, error) {
		values["removed"] = 1
		values["retained"] = 2
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}

	expectedErr := errors.New("mutation failed after change")
	err := state.Apply(t.Context(), "partial mutation", func(values map[string]int) (bool, error) {
		delete(values, "removed")
		return true, expectedErr
	})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("Apply error = %v, want %v", err, expectedErr)
	}
	if _, found := state.Get("removed"); found {
		t.Fatal("removed key is still published")
	}
	if retained, found := state.Get("retained"); !found || retained != 2 {
		t.Fatalf("Get(retained) = (%d, %t), want (2, true)", retained, found)
	}
}

func TestStateMapPublishesPartialMutationBeforePropagatingPanic(t *testing.T) {
	state := NewStateMap[string, int]()
	if err := state.Apply(t.Context(), "seed", func(values map[string]int) (bool, error) {
		values["removed"] = 1
		return true, nil
	}); err != nil {
		t.Fatal(err)
	}

	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Apply did not propagate the panic")
			}
		}()
		_ = state.Apply(t.Context(), "panic after mutation", func(values map[string]int) (bool, error) {
			delete(values, "removed")
			panic("mutation panic")
		})
	}()
	if _, found := state.Get("removed"); found {
		t.Fatal("removed key is still published")
	}
}
