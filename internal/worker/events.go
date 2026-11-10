package worker

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/skael-dev/skael/internal/evalqueue"
)

// eventFlushEvery is how often buffered job events are posted.
const eventFlushEvery = 2 * time.Second

// eventSink logs each job event as a structured line and posts them to the
// server in batches, so `kubectl logs` and the Jobs page tell one story. A
// failed post is logged and never stops the run.
type eventSink struct {
	api   API
	id    evalqueue.JobID
	token string

	mu    sync.Mutex
	buf   []evalqueue.EvalJobEvent
	done  chan struct{}
	wg    sync.WaitGroup
	close sync.Once
}

func newEventSink(api API, id evalqueue.JobID, token string) *eventSink {
	s := &eventSink{api: api, id: id, token: token, done: make(chan struct{})}
	s.wg.Add(1)
	go s.loop()
	return s
}

// Add records one event.
func (s *eventSink) Add(e evalqueue.EvalJobEvent) {
	if e.At.IsZero() {
		e.At = time.Now().UTC()
	}
	logEvent(s.id, e)
	s.mu.Lock()
	s.buf = append(s.buf, e)
	s.mu.Unlock()
}

func (s *eventSink) loop() {
	defer s.wg.Done()
	t := time.NewTicker(eventFlushEvery)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			s.flush()
			return
		case <-t.C:
			s.flush()
		}
	}
}

func (s *eventSink) flush() {
	s.mu.Lock()
	batch := s.buf
	s.buf = nil
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := s.api.PostEvents(ctx, s.id, s.token, batch); err != nil {
		log.Warn().Err(err).Str("job_id", string(s.id)).Int("events", len(batch)).Msg("worker: posting job events failed")
	}
}

// Close posts what is left. Call it before the job's report or failure, so
// the last events precede the outcome.
func (s *eventSink) Close() {
	s.close.Do(func() {
		close(s.done)
		s.wg.Wait()
	})
}

func logEvent(id evalqueue.JobID, e evalqueue.EvalJobEvent) {
	lvl := zerolog.InfoLevel
	if e.Error != "" {
		lvl = zerolog.WarnLevel
	}
	l := log.WithLevel(lvl).Str("job_id", string(id)).Str("event", e.Kind)
	str := func(k, v string) {
		if v != "" {
			l = l.Str(k, v)
		}
	}
	str("phase", e.Phase)
	str("task", e.Task)
	str("condition", e.Condition)
	str("model", e.Model)
	str("status", e.Status)
	str("error", e.Error)
	if e.Total > 0 {
		l = l.Int("total", e.Total)
	}
	if e.OK != nil {
		l = l.Bool("ok", *e.OK)
	}
	if e.DurationMS > 0 {
		l = l.Int64("duration_ms", e.DurationMS)
	}
	if e.CostUSD > 0 {
		l = l.Float64("cost_usd", e.CostUSD)
	}
	l.Msg("worker: job event")
}

type eventsKey struct{}

// withEvents carries a job's event recorder through the run.
func withEvents(ctx context.Context, add func(evalqueue.EvalJobEvent)) context.Context {
	return context.WithValue(ctx, eventsKey{}, add)
}

// eventsFrom is the job's event recorder, or one that only logs.
func eventsFrom(ctx context.Context) func(evalqueue.EvalJobEvent) {
	if add, ok := ctx.Value(eventsKey{}).(func(evalqueue.EvalJobEvent)); ok {
		return add
	}
	return func(e evalqueue.EvalJobEvent) { logEvent("", e) }
}

func phase(name string) evalqueue.EvalJobEvent {
	return evalqueue.EvalJobEvent{Kind: evalqueue.EventPhase, Phase: name}
}
