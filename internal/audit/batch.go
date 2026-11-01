package audit

import (
	"context"
	"time"
)

// loop collects queued rows and writes them when a batch fills up or the interval elapses.
// It exits after the queue is closed and drained.
func (r *Recorder) loop() {
	defer close(r.done)
	ticker := time.NewTicker(r.opts.FlushInterval)
	defer ticker.Stop()
	batch := make([]row, 0, r.opts.BatchSize)
	for {
		select {
		case rw, ok := <-r.queue:
			if !ok {
				r.flushBatch(batch)
				return
			}
			batch = append(batch, rw)
			if len(batch) >= r.opts.BatchSize {
				r.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			r.flushBatch(batch)
			batch = batch[:0]
		}
	}
}

// flushBatch writes a batch, retrying once. If the batch still fails it falls back to one write
// per row so a single bad row cannot sink the others; rows that still fail are dropped and the
// number is logged.
func (r *Recorder) flushBatch(batch []row) {
	if len(batch) == 0 {
		return
	}
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err = r.flush(ctx, batch)
		cancel()
		if err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	dropped := r.flushRowByRow(batch)
	r.log.Error("audit batch write failed, rows retried one by one", "events", len(batch), "dropped", dropped, "error", err)
}

// flushRowByRow writes each row on its own under one shared deadline and returns how many failed.
func (r *Recorder) flushRowByRow(batch []row) (dropped int) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	for _, rw := range batch {
		if err := r.flush(ctx, []row{rw}); err != nil {
			dropped++
		}
	}
	return dropped
}
