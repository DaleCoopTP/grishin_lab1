package positions

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestQueueDeduplicatesAndCancels(t *testing.T) {
	q := newQueue(2)
	a, b, c := uuid.New(), uuid.New(), uuid.New()
	for _, step := range []struct {
		trip uuid.UUID
		want string
	}{
		{a, ResultQueued}, {a, ResultDuplicate}, {b, ResultQueued}, {c, ResultDropped},
	} {
		if got := q.offer(task{tripID: step.trip}); got != step.want {
			t.Fatalf("offer %s: got %s, want %s", step.trip, got, step.want)
		}
	}
	if _, queued := q.pending[c]; queued {
		t.Fatal("dropped trip must not stay in the pending set")
	}

	q.cancel(a)
	ctx := context.Background()
	first, _ := q.take(ctx)
	if first.tripID != a || q.claim(first) {
		t.Fatal("cancelled task must not be claimed")
	}
	if got := q.offer(task{tripID: a}); got != ResultQueued {
		t.Fatalf("offer after cancel: %s", got)
	}
	q.done(first)
	if _, queued := q.pending[a]; !queued {
		t.Fatal("finishing a stale task removed the new entry")
	}

	second, _ := q.take(ctx)
	if second.tripID != b || !q.claim(second) {
		t.Fatal("live task must be claimed")
	}
	if q.claim(second) {
		t.Fatal("a task must not be claimed twice")
	}
	q.cancel(b)
	if got := q.offer(task{tripID: b}); got != ResultDuplicate {
		t.Fatalf("trip being processed must stay pending even after cancel: %s", got)
	}
	q.done(second)
	if got := q.offer(task{tripID: b}); got != ResultQueued {
		t.Fatalf("offer after done: %s", got)
	}
	if got := q.offer(task{tripID: c}); got != ResultDropped || q.len() != 2 {
		t.Fatalf("queue must be full: %s, len %d", got, q.len())
	}

	q.close()
	q.close()
	if got := q.offer(task{tripID: uuid.New()}); got != ResultDropped {
		t.Fatalf("offer after close: %s", got)
	}
	for _, want := range []uuid.UUID{a, b} {
		next, ok := q.take(ctx)
		if !ok || next.tripID != want || !q.claim(next) {
			t.Fatal("tasks queued before close must still be delivered in order")
		}
	}
	if _, ok := q.take(ctx); ok {
		t.Fatal("take from a closed, drained queue must report false")
	}
}

func TestQueueTakeStopsOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := newQueue(1).take(ctx); ok {
		t.Fatal("take must stop when the context is cancelled")
	}
}

func TestQueueConcurrentAccess(t *testing.T) {
	q := newQueue(64)
	trips := make([]uuid.UUID, 8)
	for i := range trips {
		trips[i] = uuid.New()
	}
	ctx, cancel := context.WithCancel(context.Background())
	var producers, consumers sync.WaitGroup
	for i := range 4 {
		producers.Go(func() {
			for n := range 500 {
				id := trips[(i+n)%len(trips)]
				q.offer(task{tripID: id})
				if n%7 == 0 {
					q.cancel(id)
				}
			}
		})
	}
	var inFlight sync.Map
	for range 4 {
		consumers.Go(func() {
			for {
				t2, ok := q.take(ctx)
				if !ok {
					return
				}
				if !q.claim(t2) {
					continue
				}
				if _, loaded := inFlight.LoadOrStore(t2.tripID, struct{}{}); loaded {
					t.Errorf("trip %s processed by two workers at once", t2.tripID)
				}
				inFlight.Delete(t2.tripID)
				q.done(t2)
			}
		})
	}
	producers.Wait()
	q.close()
	consumers.Wait()
	cancel()
}
