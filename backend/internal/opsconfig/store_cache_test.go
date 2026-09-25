package opsconfig

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStoreValueCoalescesConcurrentLoads(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var calls atomic.Int64
	store := testStore(func(context.Context, string) (json.RawMessage, error) {
		calls.Add(1)
		once.Do(func() { close(started) })
		<-release
		return json.RawMessage(`{"maintenance":true}`), nil
	})

	const readers = 32
	errors := make(chan error, readers)
	var group sync.WaitGroup
	for range readers {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := store.value(context.Background(), "flags")
			errors <- err
		}()
	}
	<-started
	close(release)
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("load cached value: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("database loads = %d, want 1", got)
	}
}

func TestStoreInvalidateDoesNotRecacheStaleLoad(t *testing.T) {
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	var calls atomic.Int64
	store := testStore(func(context.Context, string) (json.RawMessage, error) {
		switch calls.Add(1) {
		case 1:
			close(oldStarted)
			<-releaseOld
			return json.RawMessage(`{"version":"old"}`), nil
		default:
			return json.RawMessage(`{"version":"new"}`), nil
		}
	})

	oldResult := make(chan error, 1)
	go func() {
		_, err := store.value(context.Background(), "flags")
		oldResult <- err
	}()
	<-oldStarted
	store.Invalidate("flags")

	raw, err := store.value(context.Background(), "flags")
	if err != nil {
		t.Fatalf("load invalidated value: %v", err)
	}
	if string(raw) != `{"version":"new"}` {
		t.Fatalf("invalidated value = %s, want new", raw)
	}
	close(releaseOld)
	if err := <-oldResult; err != nil {
		t.Fatalf("finish stale load: %v", err)
	}

	raw, err = store.value(context.Background(), "flags")
	if err != nil {
		t.Fatalf("read refreshed cache: %v", err)
	}
	if string(raw) != `{"version":"new"}` {
		t.Fatalf("cached value = %s, stale load overwrote refresh", raw)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("database loads = %d, want 2", got)
	}
}

func testStore(load func(context.Context, string) (json.RawMessage, error)) *Store {
	return &Store{
		ttl: time.Minute, now: time.Now, load: load,
		cache: make(map[string]cachedValue), generations: make(map[string]uint64),
	}
}
