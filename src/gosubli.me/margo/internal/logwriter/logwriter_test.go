package logwriter

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
)

// A Syncer is a spy for the Sync portion of zapcore.WriteSyncer.
type Syncer struct {
	err    error
	called bool
}

// SetError sets the error that the Sync method will return.
func (s *Syncer) SetError(err error) {
	s.err = err
}

// Sync records that it was called, then returns the user-supplied error (if
// any).
func (s *Syncer) Sync() error {
	s.called = true
	return s.err
}

// Called reports whether the Sync method was called.
func (s *Syncer) Called() bool {
	return s.called
}

// FailWriter is a WriteSyncer that always returns an error on writes.
type FailWriter struct{ Syncer }

// Write implements io.Writer.
func (w FailWriter) Write(b []byte) (int, error) {
	return len(b), errors.New("failed")
}

func requireWriteWorks(t testing.TB, ws zapcore.WriteSyncer) {
	n, err := ws.Write([]byte("foo"))
	require.NoError(t, err, "Unexpected error writing to WriteSyncer.")
	require.Equal(t, 3, n, "Wrote an unexpected number of bytes.")
}

func TestBufferWriter(t *testing.T) {
	// If we pass a plain io.Writer, make sure that we still get a WriteSyncer
	// with a no-op Sync.
	t.Run("sync", func(t *testing.T) {
		buf := &bytes.Buffer{}
		ws := &BufferedWriteSyncer{WS: zapcore.AddSync(buf)}

		requireWriteWorks(t, ws)
		assert.Empty(t, buf.String(), "Unexpected log calling a no-op Write method.")
		assert.NoError(t, ws.Sync(), "Unexpected error calling a no-op Sync method.")
		assert.Equal(t, "foo", buf.String(), "Unexpected log string")
		assert.NoError(t, ws.Stop())
	})

	t.Run("stop", func(t *testing.T) {
		buf := &bytes.Buffer{}
		ws := &BufferedWriteSyncer{WS: zapcore.AddSync(buf)}
		requireWriteWorks(t, ws)
		assert.Empty(t, buf.String(), "Unexpected log calling a no-op Write method.")
		assert.NoError(t, ws.Stop())
		assert.Equal(t, "foo", buf.String(), "Unexpected log string")
	})

	t.Run("stop twice", func(t *testing.T) {
		ws := &BufferedWriteSyncer{WS: &FailWriter{}}
		_, err := ws.Write([]byte("foo"))
		require.NoError(t, err, "Unexpected error writing to WriteSyncer.")
		assert.Error(t, ws.Stop(), "Expected stop to fail.")
		assert.NoError(t, ws.Stop(), "Expected stop to not fail.")
	})

	t.Run("wrap twice", func(t *testing.T) {
		buf := &bytes.Buffer{}
		bufsync := &BufferedWriteSyncer{WS: zapcore.AddSync(buf)}
		ws := &BufferedWriteSyncer{WS: bufsync}
		requireWriteWorks(t, ws)
		assert.Empty(t, buf.String(), "Unexpected log calling a no-op Write method.")
		require.NoError(t, ws.Sync())
		assert.Equal(t, "foo", buf.String())
		assert.NoError(t, ws.Stop())
		assert.NoError(t, bufsync.Stop())
		assert.Equal(t, "foo", buf.String(), "Unexpected log string")
	})

	t.Run("small buffer", func(t *testing.T) {
		buf := &bytes.Buffer{}
		ws := &BufferedWriteSyncer{WS: zapcore.AddSync(buf), Size: 5}

		requireWriteWorks(t, ws)
		assert.Equal(t, "", buf.String(), "Unexpected log calling a no-op Write method.")
		requireWriteWorks(t, ws)
		assert.Equal(t, "foo", buf.String(), "Unexpected log string")
		assert.NoError(t, ws.Stop())
	})

	t.Run("with lockedWriteSyncer", func(t *testing.T) {
		buf := &bytes.Buffer{}
		ws := &BufferedWriteSyncer{WS: zapcore.Lock(zapcore.AddSync(buf)), Size: 5}

		requireWriteWorks(t, ws)
		assert.Equal(t, "", buf.String(), "Unexpected log calling a no-op Write method.")
		requireWriteWorks(t, ws)
		assert.Equal(t, "foo", buf.String(), "Unexpected log string")
		assert.NoError(t, ws.Stop())
	})

	t.Run("flush error", func(t *testing.T) {
		ws := &BufferedWriteSyncer{WS: &FailWriter{}, Size: 4}
		n, err := ws.Write([]byte("foo"))
		require.NoError(t, err, "Unexpected error writing to WriteSyncer.")
		require.Equal(t, 3, n, "Wrote an unexpected number of bytes.")
		ws.Write([]byte("foo"))
		assert.Error(t, ws.Stop(), "Expected stop to fail.")
	})

	// t.Run("flush timer", func(t *testing.T) {
	// 	buf := &bytes.Buffer{}
	// 	clock := ztest.NewMockClock()
	// 	ws := &BufferedWriteSyncer{
	// 		WS:            zapcore.AddSync(buf),
	// 		Size:          6,
	// 		FlushInterval: time.Microsecond,
	// 		Clock:         clock,
	// 	}
	// 	requireWriteWorks(t, ws)
	// 	clock.Add(10 * time.Microsecond)
	// 	assert.Equal(t, "foo", buf.String(), "Unexpected log string")

	// 	// flush twice to validate loop logic
	// 	requireWriteWorks(t, ws)
	// 	clock.Add(10 * time.Microsecond)
	// 	assert.Equal(t, "foofoo", buf.String(), "Unexpected log string")
	// 	assert.NoError(t, ws.Stop())
	// })
}

func TestBufferWriterWithoutStart(t *testing.T) {
	t.Run("stop", func(t *testing.T) {
		ws := &BufferedWriteSyncer{WS: zapcore.AddSync(new(bytes.Buffer))}
		assert.NoError(t, ws.Stop(), "Stop must not fail")
	})

	t.Run("Sync", func(t *testing.T) {
		ws := &BufferedWriteSyncer{WS: zapcore.AddSync(new(bytes.Buffer))}
		assert.NoError(t, ws.Sync(), "Sync must not fail")
	})
}

func BenchmarkBufferedWriteSyncer_Base(b *testing.B) {
	b.Run("write file with buffer", func(b *testing.B) {
		file, err := ioutil.TempFile("", "log")
		require.NoError(b, err)

		defer func() {
			assert.NoError(b, file.Close())
			assert.NoError(b, os.Remove(file.Name()))
		}()

		w := &zapcore.BufferedWriteSyncer{
			WS: zapcore.AddSync(file),
		}
		defer w.Stop()
		b.SetBytes(int64(len("foobarbazbabble")))
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				w.Write([]byte("foobarbazbabble"))
			}
		})
	})
}

func BenchmarkBufferedWriteSyncer(b *testing.B) {
	b.Run("write file with buffer", func(b *testing.B) {
		file, err := ioutil.TempFile("", "log")
		require.NoError(b, err)

		defer func() {
			assert.NoError(b, file.Close())
			assert.NoError(b, os.Remove(file.Name()))
		}()

		w := &BufferedWriteSyncer{
			WS: zapcore.AddSync(file),
		}
		defer w.Stop()
		b.SetBytes(int64(len("foobarbazbabble")))
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				w.Write([]byte("foobarbazbabble"))
			}
		})
	})
}
