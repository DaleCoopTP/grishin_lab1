package positions

import (
	"context"
	"sync"

	"github.com/google/uuid"
)

const (
	ResultQueued    = "queued"
	ResultDuplicate = "duplicate"
	ResultDropped   = "dropped"
	ResultSuccess   = "success"
	ResultError     = "error"
)

type task struct {
	tripID   uuid.UUID
	driverID uuid.UUID
	seq      uint64
}

// queue is a bounded channel plus the set of trips that are "in the queue now".
// A trip stays in the set from offer until a worker finishes it or a new
// position cancels it. Every entry has its own sequence number, so a task that
// was cancelled and then queued again is not mistaken for the new one.
type queue struct {
	mu      sync.Mutex
	tasks   chan task
	pending map[uuid.UUID]uint64
	seq     uint64
	closed  bool
}

func newQueue(size int) *queue {
	return &queue{tasks: make(chan task, size), pending: make(map[uuid.UUID]uint64, size)}
}

// offer never blocks. The duplicate check and the channel send happen under one
// lock, so two offers of the same trip cannot both pass the check.
func (q *queue) offer(t task) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return ResultDropped
	}
	if _, ok := q.pending[t.tripID]; ok {
		return ResultDuplicate
	}
	q.seq++
	t.seq = q.seq
	select {
	case q.tasks <- t:
		q.pending[t.tripID] = t.seq
		return ResultQueued
	default:
		return ResultDropped
	}
}

// take waits for the next task; false means the queue is closed and drained or
// ctx is done.
func (q *queue) take(ctx context.Context) (task, bool) {
	select {
	case <-ctx.Done():
		return task{}, false
	case t, ok := <-q.tasks:
		return t, ok
	}
}

// claim reports whether the task is still wanted, i.e. no position arrived
// since it was queued.
func (q *queue) claim(t task) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	seq, ok := q.pending[t.tripID]
	return ok && seq == t.seq
}

func (q *queue) done(t task) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if seq, ok := q.pending[t.tripID]; ok && seq == t.seq {
		delete(q.pending, t.tripID)
	}
}

func (q *queue) cancel(tripID uuid.UUID) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.pending, tripID)
}

func (q *queue) len() int {
	return len(q.tasks)
}

func (q *queue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.tasks)
	}
}
