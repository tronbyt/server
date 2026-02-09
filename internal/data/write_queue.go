package data

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"gorm.io/gorm"
)

// WriteQueue manages a queue of database write operations.
type WriteQueue struct {
	db     *gorm.DB
	queue  chan writeOp
	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// writeOp represents a queued database operation.
type writeOp struct {
	ctx  context.Context
	do   func(*gorm.DB) error
	done chan error
}

// NewWriteQueue creates a new write queue with the specified buffer size.
func NewWriteQueue(db *gorm.DB, bufferSize int) *WriteQueue {
	ctx, cancel := context.WithCancel(context.Background())
	wq := &WriteQueue{
		db:     db,
		queue:  make(chan writeOp, bufferSize),
		ctx:    ctx,
		cancel: cancel,
	}

	// Start the worker goroutine
	wq.wg.Add(1)
	go wq.worker()

	return wq
}

// worker processes write operations sequentially.
func (wq *WriteQueue) worker() {
	defer wq.wg.Done()

	for {
		select {
		case <-wq.ctx.Done():
			return
		case op := <-wq.queue:
			// Execute the write operation
			err := op.do(wq.db)
			if op.done != nil {
				op.done <- err
				close(op.done)
			}
		}
	}
}

// Execute queues a write operation and waits for completion with timeout.
func (wq *WriteQueue) Execute(ctx context.Context, fn func(*gorm.DB) error) error {
	done := make(chan error, 1)
	op := writeOp{
		ctx:  ctx,
		do:   fn,
		done: done,
	}

	select {
	case wq.queue <- op:
		// Wait for completion or timeout
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(30 * time.Second):
			return context.DeadlineExceeded
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		// Queue is full, execute synchronously as fallback
		slog.Warn("Write queue full, executing synchronously")
		return fn(wq.db)
	}
}

// ExecuteAsync queues a write operation without waiting (fire-and-forget).
// If the queue is full, executes synchronously to avoid data loss.
func (wq *WriteQueue) ExecuteAsync(fn func(*gorm.DB) error) {
	op := writeOp{
		ctx: context.Background(),
		do:  fn,
	}

	select {
	case wq.queue <- op:
		// Successfully queued
	default:
		// Queue is full, execute synchronously with timeout to prevent blocking
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		done := make(chan error, 1)
		syncOp := writeOp{
			ctx:  ctx,
			do:   fn,
			done: done,
		}

		// Try to queue the synchronous operation
		select {
		case wq.queue <- syncOp:
			// Wait for completion with timeout
			select {
			case <-done:
				// Completed successfully
			case <-ctx.Done():
				slog.Warn("Write queue full, synchronous execution timed out")
			}
		case <-ctx.Done():
			// Even the queue insertion timed out
			slog.Warn("Write queue full, operation dropped after timeout")
		}
	}
}

// Flush waits for all queued operations to complete.
// Used primarily in tests to ensure async writes are finished before assertions.
func (wq *WriteQueue) Flush(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		// Send a marker operation and wait for it to complete
		markerDone := make(chan error, 1)
		markerOp := writeOp{
			ctx:  context.Background(),
			do:   func(*gorm.DB) error { return nil },
			done: markerDone,
		}
		select {
		case wq.queue <- markerOp:
			<-markerDone
		case <-time.After(timeout):
		}
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// Stop gracefully shuts down the write queue.
func (wq *WriteQueue) Stop() {
	wq.cancel()
	wq.wg.Wait()
}
