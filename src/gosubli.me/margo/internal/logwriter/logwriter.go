package logwriter

import (
	"bufio"
	"net/url"
	"os"
	"sync"
	"time"

	"go.uber.org/multierr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func init() {
	zap.RegisterSink("async:stderr", func(*url.URL) (zap.Sink, error) {
		s := &BufferedWriteSyncer{
			WS: zapcore.AddSync(os.Stderr),
		}
		return s, nil
	})
}

type BufferedWriteSyncer struct {
	WS            zapcore.WriteSyncer
	Size          int
	FlushInterval time.Duration

	// unexported fields for state
	mu          sync.Mutex
	initialized bool // whether initialize() has run
	stopped     bool // whether Stop() has run
	writer      *bufio.Writer
	ticker      *time.Ticker
	stop        chan struct{} // closed when flushLoop should stop
	done        chan struct{} // closed when flushLoop has stopped}
	flush       chan struct{}
}

func (s *BufferedWriteSyncer) initialize() {
	size := s.Size
	if size <= 0 {
		size = 8 * 1024
	}
	flushInterval := s.FlushInterval
	if flushInterval <= 0 {
		flushInterval = time.Second * 10
	}
	s.ticker = time.NewTicker(flushInterval)
	s.writer = bufio.NewWriterSize(s.WS, size)
	s.stop = make(chan struct{})
	s.done = make(chan struct{})
	s.flush = make(chan struct{}, 1)
	s.initialized = true

	go s.flushLoop()
}

func (s *BufferedWriteSyncer) Write(b []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.initialized {
		s.initialize()
	}

	if len(b) > s.writer.Available() && s.writer.Buffered() > 0 {
		if err := s.writer.Flush(); err != nil {
			return 0, err
		}
	}

	n, err := s.writer.Write(b)
	select {
	case s.flush <- struct{}{}:
	default:
	}
	return n, err
}

// Sync flushes buffered log data into disk directly.
func (s *BufferedWriteSyncer) Sync() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var err error
	if s.initialized {
		err = s.writer.Flush()
	}

	return multierr.Append(err, s.WS.Sync())
}

func (s *BufferedWriteSyncer) flushNoSync() {
	if s.mu.TryLock() {
		defer s.mu.Unlock()
		if s.initialized && s.writer.Buffered() != 0 {
			_ = s.writer.Flush()
		}
	} else if len(s.flush) == 0 {
		// re-queue a flush
		select {
		case s.flush <- struct{}{}:
		default:
		}
	}
}

func (s *BufferedWriteSyncer) flushLoop() {
	defer close(s.done)
	for {
		select {
		case <-s.flush:
			s.flushNoSync()
		case <-s.ticker.C:
			_ = s.Sync()
		case <-s.stop:
			return
		}
	}
}

// Stop closes the buffer, cleans up background goroutines, and flushes
// remaining unwritten data.
func (s *BufferedWriteSyncer) Stop() (err error) {
	var stopped bool

	// Critical section.
	func() {
		s.mu.Lock()
		defer s.mu.Unlock()

		if !s.initialized {
			return
		}

		stopped = s.stopped
		if stopped {
			return
		}
		s.stopped = true

		s.ticker.Stop()
		close(s.stop) // tell flushLoop to stop
		// close(s.flush) // close flush chan
		<-s.done // and wait until it has
	}()

	// Don't call Sync on consecutive Stops.
	if !stopped {
		err = s.Sync()
	}

	return err
}

func (s *BufferedWriteSyncer) Close() error {
	return s.Stop()
}
