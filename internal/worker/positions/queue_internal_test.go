package positions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func silentFor(d time.Duration) time.Time {
	return time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC).Add(-d)
}

func newTask(d time.Duration) task {
	return task{tripID: uuid.New(), lastSeenAt: silentFor(d)}
}

func mustTake(t *testing.T, q *queue) task {
	t.Helper()
	next, ok := q.take(context.Background())
	if !ok {
		t.Fatal("queue is unexpectedly empty")
	}
	return next
}

func TestQueueOrdersByLastSeen(t *testing.T) {
	q := newQueue(3)
	recent, oldest, middle := newTask(10*time.Minute), newTask(30*time.Minute), newTask(20*time.Minute)
	for _, tk := range []task{recent, oldest, middle} {
		if got := q.offer(tk); got.result != ResultQueued || got.evicted {
			t.Fatalf("offer: %+v", got)
		}
	}
	for _, want := range []task{oldest, middle, recent} {
		if got := mustTake(t, q); got.tripID != want.tripID {
			t.Fatalf("took %s silent since %s, want %s", got.tripID, got.lastSeenAt, want.tripID)
		}
	}
}

func TestQueueTiesKeepQueueingOrder(t *testing.T) {
	q := newQueue(3)
	first, second := newTask(time.Hour), newTask(time.Hour)
	q.offer(first)
	q.offer(second)
	if got := mustTake(t, q); got.tripID != first.tripID {
		t.Fatal("trips silent for the same time must keep the queueing order")
	}
}

func TestQueueEvictsFreshestWhenFull(t *testing.T) {
	q := newQueue(2)
	freshest, fresh := newTask(5*time.Minute), newTask(6*time.Minute)
	q.offer(freshest)
	q.offer(fresh)
	if got := q.offer(newTask(time.Minute)); got.result != ResultDropped || got.evicted {
		t.Fatalf("fresher trip must be dropped: %+v", got)
	}
	if got := q.offer(newTask(5 * time.Minute)); got.result != ResultDropped {
		t.Fatalf("trip as fresh as the freshest must be dropped: %+v", got)
	}
	old := newTask(time.Hour)
	if got := q.offer(old); got.result != ResultQueued || !got.evicted {
		t.Fatalf("older trip must evict the freshest: %+v", got)
	}
	if _, ok := q.pending[freshest.tripID]; ok {
		t.Fatal("evicted trip must leave the pending set")
	}
	if got := q.offer(freshest); got.result != ResultDropped {
		t.Fatalf("evicted trip can be queued again only if older than the freshest: %+v", got)
	}
	if q.len() != 2 || mustTake(t, q).tripID != old.tripID || mustTake(t, q).tripID != fresh.tripID {
		t.Fatal("queue must hold the old and the fresh trip")
	}
}

func TestQueueDeduplicatesAndCancels(t *testing.T) {
	q := newQueue(3)
	a, b := newTask(30*time.Minute), newTask(20*time.Minute)
	q.offer(a)
	if got := q.offer(a); got.result != ResultDuplicate {
		t.Fatalf("second offer: %+v", got)
	}
	q.offer(b)

	q.cancel(a.tripID)
	if q.len() != 1 {
		t.Fatalf("cancel must remove a waiting trip, len %d", q.len())
	}
	if got := q.offer(a); got.result != ResultQueued {
		t.Fatalf("offer after cancel: %+v", got)
	}

	taken := mustTake(t, q)
	if taken.tripID != a.tripID || !q.claim(taken) || q.claim(task{tripID: a.tripID, seq: taken.seq + 100}) {
		t.Fatal("only the current entry of a trip can be claimed")
	}
	q.cancel(a.tripID)
	if got := q.offer(a); got.result != ResultDuplicate {
		t.Fatalf("trip being processed must stay pending even after cancel: %+v", got)
	}
	q.done(task{tripID: a.tripID, seq: taken.seq + 100})
	if got := q.offer(a); got.result != ResultDuplicate {
		t.Fatal("done with a stale task must not release the trip")
	}
	q.done(taken)
	if got := q.offer(a); got.result != ResultQueued {
		t.Fatalf("offer after done: %+v", got)
	}
}

func TestQueueClose(t *testing.T) {
	q := newQueue(2)
	a, b := newTask(time.Hour), newTask(time.Minute)
	q.offer(b)
	q.offer(a)
	q.close()
	q.close()
	if got := q.offer(newTask(2 * time.Hour)); got.result != ResultDropped {
		t.Fatalf("offer after close: %+v", got)
	}
	if mustTake(t, q).tripID != a.tripID || mustTake(t, q).tripID != b.tripID {
		t.Fatal("tasks queued before close must still be delivered by priority")
	}
	if _, ok := q.take(context.Background()); ok {
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

func TestQueueTakeWaitsForOffer(t *testing.T) {
	q := newQueue(1)
	got := make(chan task)
	go func() {
		next, _ := q.take(context.Background())
		got <- next
	}()
	want := newTask(time.Hour)
	q.offer(want)
	if (<-got).tripID != want.tripID {
		t.Fatal("waiting worker must receive the offered task")
	}
}

func TestQueueConcurrentAccess(t *testing.T) {
	q := newQueue(16)
	trips := make([]uuid.UUID, 32)
	for i := range trips {
		trips[i] = uuid.New()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var producers, consumers sync.WaitGroup
	for i := range 4 {
		producers.Go(func() {
			for n := range 1000 {
				id := trips[(i*7+n)%len(trips)]
				q.offer(task{tripID: id, lastSeenAt: silentFor(time.Duration((i*7919+n*104729)%3600) * time.Second)})
				if n%5 == 0 {
					q.cancel(id)
				}
				_ = q.len()
			}
		})
	}
	var inFlight sync.Map
	for range 4 {
		consumers.Go(func() {
			for {
				next, ok := q.take(ctx)
				if !ok {
					return
				}
				if !q.claim(next) {
					t.Errorf("taken task %s cannot be claimed", next.tripID)
				}
				if _, loaded := inFlight.LoadOrStore(next.tripID, struct{}{}); loaded {
					t.Errorf("trip %s processed by two workers at once", next.tripID)
				}
				inFlight.Delete(next.tripID)
				q.done(next)
			}
		})
	}
	producers.Wait()
	q.close()
	consumers.Wait()
	if len(q.pending) != 0 || q.len() != 0 {
		t.Fatalf("queue left %d pending trips and %d waiting", len(q.pending), q.len())
	}
}
