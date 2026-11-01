package concurrency

import (
	"context"
	"errors"
	"testing"
	"time"
)

func deliver(t *testing.T, gate *SyncGate, scope, key string, body []byte) bool {
	t.Helper()
	unchanged, finish, err := gate.Begin(t.Context(), scope, key, body)
	if err != nil {
		t.Fatal(err)
	}
	finish(!unchanged)
	return unchanged
}

func expectDelivery(t *testing.T, got, want bool, message string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: unchanged = %t, want %t", message, got, want)
	}
}

func TestSyncGate_TracksLastDeliveredPayloadPerScopeAndKey(t *testing.T) {
	var gate SyncGate
	body := []byte(`{"registries":[]}`)

	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), false, "nothing delivered yet")
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), true, "same body")
	expectDelivery(t, deliver(t, &gate, "env", "/registries", []byte(`{"registries":[{"id":"a"}]}`)), false, "new body")
	expectDelivery(t, deliver(t, &gate, "env", "/s3", body), false, "keys are independent")
	expectDelivery(t, deliver(t, &gate, "other", "/registries", body), false, "scopes are independent")

	gate.Forget("env")
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), false, "forget drops every key in the scope")
	gate.Forget("missing")
}

func TestSyncGate_FailedDeliveryIsNotRemembered(t *testing.T) {
	var gate SyncGate
	body := []byte(`{"registries":[]}`)
	unchanged, finish, err := gate.Begin(t.Context(), "env", "/registries", body)
	if err != nil {
		t.Fatal(err)
	}
	expectDelivery(t, unchanged, false, "first begin")
	finish(false)
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), false, "failed delivery")
}

func TestSyncGate_ForgetDiscardsInFlightDelivery(t *testing.T) {
	var gate SyncGate
	body := []byte(`{"registries":[]}`)

	_, finish, err := gate.Begin(t.Context(), "env", "/registries", body)
	if err != nil {
		t.Fatal(err)
	}
	gate.Forget("env")
	finish(true)
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), false, "a delivery that started before Forget must not count")
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), true, "redelivery")
}

func TestSyncGate_SerializesDeliveriesPerKey(t *testing.T) {
	var gate SyncGate
	older, newer := []byte(`{"v":1}`), []byte(`{"v":2}`)
	_, finishOlder, err := gate.Begin(t.Context(), "env", "/registries", older)
	if err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	done := make(chan bool)
	go func() {
		close(started)
		unchanged, finish, beginErr := gate.Begin(t.Context(), "env", "/registries", newer)
		if beginErr != nil {
			t.Error(beginErr)
			done <- false
			return
		}
		finish(true)
		done <- unchanged
	}()
	<-started
	select {
	case <-done:
		t.Fatal("the second delivery must wait for the first to finish")
	case <-time.After(50 * time.Millisecond):
	}
	finishOlder(true)
	expectDelivery(t, <-done, false, "second delivery")
	expectDelivery(t, deliver(t, &gate, "env", "/registries", newer), true, "the last delivery wins")
}

func TestSyncGate_BeginStopsWaitingWhenContextEnds(t *testing.T) {
	var gate SyncGate
	body := []byte(`{"v":1}`)
	_, finish, err := gate.Begin(t.Context(), "env", "/registries", body)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if _, _, err = gate.Begin(ctx, "env", "/registries", body); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Begin error = %v, want deadline exceeded", err)
	}

	finish(true)
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), true, "the key is usable after an abandoned wait")
}

func TestSyncGate_ExpiryForcesPeriodicResend(t *testing.T) {
	gate := SyncGate{Expiry: time.Hour}
	body := []byte(`{"registries":[]}`)
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), false, "first delivery")
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), true, "second delivery")

	gate.sent["env"]["/registries"] = syncGateEntry{digest: gate.sent["env"]["/registries"].digest, sentAt: time.Now().Add(-2 * time.Hour)}
	expectDelivery(t, deliver(t, &gate, "env", "/registries", body), false, "an old delivery is no longer trusted")
}
