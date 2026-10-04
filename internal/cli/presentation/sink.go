package presentation

import (
	"io"
	"sync"
)

type terminalSink struct {
	mu       sync.Mutex
	out      io.Writer
	firstErr error
}

func newTerminalSink(out io.Writer) *terminalSink {
	if out == nil {
		out = io.Discard
	}
	return &terminalSink{out: out}
}

func (sink *terminalSink) writeString(value string) (int, error) {
	if sink == nil {
		return len(value), nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	n, err := io.WriteString(sink.out, value)
	if err != nil && sink.firstErr == nil {
		sink.firstErr = err
	}
	return n, err
}

func (sink *terminalSink) line(value string) {
	_, _ = sink.writeString(value + "\n")
}

func (sink *terminalSink) err() error {
	if sink == nil {
		return nil
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.firstErr
}
