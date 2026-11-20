package positions

import (
	"container/heap"
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
)

const (
	ResultQueued    = "queued"
	ResultDuplicate = "duplicate"
	ResultDropped   = "dropped"
	ResultSuccess   = "success"
	ResultError     = "error"
)

type task struct {
	tripID     uuid.UUID
	driverID   uuid.UUID
	lastSeenAt time.Time
	scan       trace.SpanContext
	seq        uint64
}

type queue struct {
	mu        sync.Mutex
	waiting   taskHeap
	pending   map[uuid.UUID]*entry
	capacity  int
	seq       uint64
	closed    bool
	available chan struct{}
}

type entry struct {
	task       *task
	index      int
	processing bool
}

type offerResult struct {
	result  string
	evicted bool
}

func newQueue(capacity int) *queue {
	return &queue{
		pending:   make(map[uuid.UUID]*entry, capacity),
		capacity:  capacity,
		available: make(chan struct{}, capacity),
	}
}

func (q *queue) offer(t task) offerResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return offerResult{result: ResultDropped}
	}
	if _, ok := q.pending[t.tripID]; ok {
		return offerResult{result: ResultDuplicate}
	}
	evicted := false
	if len(q.waiting) >= q.capacity {
		freshest := q.waiting.freshest()
		if freshest < 0 || !q.waiting[freshest].task.lastSeenAt.After(t.lastSeenAt) {
			return offerResult{result: ResultDropped}
		}
		victim := heap.Remove(&q.waiting, freshest).(*entry)
		delete(q.pending, victim.task.tripID)
		evicted = true
	}
	q.seq++
	t.seq = q.seq
	e := &entry{task: &t}
	heap.Push(&q.waiting, e)
	q.pending[t.tripID] = e
	q.signal()
	return offerResult{result: ResultQueued, evicted: evicted}
}

func (q *queue) signal() {
	select {
	case q.available <- struct{}{}:
	default:
	}
}

func (q *queue) take(ctx context.Context) (task, bool) {
	for {
		q.mu.Lock()
		if len(q.waiting) > 0 {
			e := heap.Pop(&q.waiting).(*entry)
			e.processing = true
			q.mu.Unlock()
			return *e.task, true
		}
		closed := q.closed
		q.mu.Unlock()
		if closed {
			return task{}, false
		}
		select {
		case <-ctx.Done():
			return task{}, false
		case <-q.available:
		}
	}
}

func (q *queue) claim(t task) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.pending[t.tripID]
	return ok && e.processing && e.task.seq == t.seq
}

func (q *queue) done(t task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if e, ok := q.pending[t.tripID]; ok && e.task.seq == t.seq {
		delete(q.pending, t.tripID)
	}
}

func (q *queue) cancel(tripID uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	e, ok := q.pending[tripID]
	if !ok || e.processing {
		return
	}
	heap.Remove(&q.waiting, e.index)
	delete(q.pending, tripID)
}

func (q *queue) len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.waiting)
}

func (q *queue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.available)
	}
}

type taskHeap []*entry

func (h taskHeap) Len() int { return len(h) }

func (h taskHeap) Less(i, j int) bool {
	a, b := h[i].task, h[j].task
	if !a.lastSeenAt.Equal(b.lastSeenAt) {
		return a.lastSeenAt.Before(b.lastSeenAt)
	}
	return a.seq < b.seq
}

func (h taskHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *taskHeap) Push(x any) {
	e := x.(*entry)
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *taskHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	old[len(old)-1] = nil
	*h = old[:len(old)-1]
	e.index = -1
	return e
}

func (h taskHeap) freshest() int {
	best := -1
	for i := range h {
		if best < 0 || h.Less(best, i) {
			best = i
		}
	}
	return best
}
