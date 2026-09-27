package atrustbackend

import (
	"bytes"
	"io"
	"log"
	"sync"
)

// The pinned upstream client narrates every request, prompt and node probe
// through the standard logger, whose default output is the terminal. That
// output is not nju-connect's to show: it would interleave with forms,
// duplicate prompts, and break the one-line stderr contract of a rejected
// credential. upstreamLog therefore owns the standard logger from the first
// upstream call on. It hands each complete line to the active stdio bridge,
// which answers prompts and detects rejections, and copies the raw output to
// a debug sink that discards it unless the host sets one.
var upstreamLog = &upstreamLogWriter{debug: io.Discard}

type upstreamLogWriter struct {
	mu       sync.Mutex
	debug    io.Writer
	pending  []byte
	observer func(line string)
}

// SetUpstreamDebugLog sets where the upstream client's raw log output goes.
// A nil writer discards it, which is the default. The output can include
// gateway messages and masked phone numbers, so a host enables it only when
// asked to.
func SetUpstreamDebugLog(output io.Writer) {
	if output == nil {
		output = io.Discard
	}
	upstreamLog.mu.Lock()
	upstreamLog.debug = output
	upstreamLog.mu.Unlock()
}

// captureUpstreamLog points the standard logger at upstreamLog. It is called
// before every upstream call, so a caller that swapped the logger output in
// between cannot let upstream lines reach the terminal. nju-connect itself
// never writes through the standard logger.
func captureUpstreamLog() {
	if log.Writer() != io.Writer(upstreamLog) {
		log.SetOutput(upstreamLog)
	}
}

// observe routes complete lines to observer until the returned function is
// called. The stdio bridge lock guarantees at most one observer at a time.
func (writer *upstreamLogWriter) observe(observer func(line string)) (stop func()) {
	writer.mu.Lock()
	writer.observer = observer
	writer.pending = writer.pending[:0]
	writer.mu.Unlock()
	return func() {
		writer.mu.Lock()
		writer.observer = nil
		writer.pending = writer.pending[:0]
		writer.mu.Unlock()
	}
}

// Write receives standard-logger output. The observer runs after the lock is
// released and must not block: the standard logger holds its own lock while
// the upstream client waits for Write to return.
func (writer *upstreamLogWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	debug, observer := writer.debug, writer.observer
	var lines []string
	if observer != nil {
		writer.pending = append(writer.pending, data...)
		for {
			index := bytes.IndexByte(writer.pending, '\n')
			if index < 0 {
				break
			}
			lines = append(lines, string(writer.pending[:index]))
			writer.pending = writer.pending[index+1:]
		}
	}
	writer.mu.Unlock()

	// A failing debug sink must not fail the upstream login.
	_, _ = debug.Write(data)
	for _, line := range lines {
		observer(line)
	}
	return len(data), nil
}
