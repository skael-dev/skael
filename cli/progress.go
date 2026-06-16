package cli

import (
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/skael-dev/skael/internal/ui"
)

var brailleFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner displays an animated braille spinner on stderr.
// It is a no-op when stderr is not a TTY or when JSON mode is active.
type Spinner struct {
	mu      sync.Mutex
	msg     string
	active  bool
	stop    chan struct{}
	stopped chan struct{}
}

// StartSpinner creates and starts a spinner with the given initial message.
// If stderr is not a terminal or JSON mode is enabled, the returned Spinner
// is inert (Update and Stop are safe no-ops).
func StartSpinner(msg string) *Spinner {
	s := &Spinner{
		msg:     msg,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	if ui.JSONMode || !isatty.IsTerminal(os.Stderr.Fd()) {
		close(s.stopped)
		return s
	}
	s.active = true
	go s.run()
	return s
}

func (s *Spinner) run() {
	defer close(s.stopped)
	frame := 0
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			fmt.Fprint(os.Stderr, "\r\033[K")
			return
		case <-ticker.C:
			s.mu.Lock()
			msg := s.msg
			s.mu.Unlock()
			fmt.Fprintf(os.Stderr, "\r\033[K  %s %s", brailleFrames[frame%len(brailleFrames)], msg)
			frame++
		}
	}
}

// Update changes the spinner message.
func (s *Spinner) Update(msg string) {
	if !s.active {
		return
	}
	s.mu.Lock()
	s.msg = msg
	s.mu.Unlock()
}

// Stop halts the spinner and clears the line.
func (s *Spinner) Stop() {
	if !s.active {
		return
	}
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
	<-s.stopped
}
