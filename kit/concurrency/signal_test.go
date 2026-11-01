package concurrency

import "testing"

func TestSignalPublishesOnceAndUnsubscribes(t *testing.T) {
	signal := NewSignal[int]()
	firstCalls := 0
	secondCalls := 0
	unsubscribeFirst := signal.Subscribe(func(value int) {
		firstCalls += value
	})
	signal.Subscribe(func(value int) {
		secondCalls += value
	})

	signal.Publish(1)
	unsubscribeFirst()
	unsubscribeFirst()
	signal.Publish(2)

	if firstCalls != 1 || secondCalls != 3 {
		t.Fatalf("calls = (%d, %d), want (1, 3)", firstCalls, secondCalls)
	}
}

func TestSignalContainsSubscriberPanic(t *testing.T) {
	signal := NewSignal[int]()
	called := false
	signal.Subscribe(func(int) { panic("deliberate signal panic") })
	signal.Subscribe(func(int) { called = true })

	signal.Publish(1)

	if !called {
		t.Fatal("subscriber after a panicking one was not called")
	}
}
