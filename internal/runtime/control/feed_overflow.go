package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"go.mewis.me/codemcp/internal/sequence"
)

type FeedOverflowError struct {
	Cause    error
	Overflow sequence.Overflow
}

func (err *FeedOverflowError) Error() string {
	if err == nil {
		return "realtime feed overflowed"
	}
	if err.Cause == nil {
		return fmt.Sprintf("realtime feed overflowed: dropped sequence %d, latest sequence %d", err.Overflow.DroppedSequence, err.Overflow.LatestSequence)
	}
	return fmt.Sprintf("%s: dropped sequence %d, latest sequence %d", err.Cause.Error(), err.Overflow.DroppedSequence, err.Overflow.LatestSequence)
}

func (err *FeedOverflowError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

func FeedOverflowOf(err error) (sequence.Overflow, bool) {
	var overflowErr *FeedOverflowError
	if !errors.As(err, &overflowErr) || overflowErr == nil {
		return sequence.Overflow{}, false
	}
	return overflowErr.Overflow, true
}

func decodeFeedOverflow(cause error, data string) error {
	overflow := sequence.Overflow{}
	if strings.TrimSpace(data) != "" {
		if err := json.Unmarshal([]byte(data), &overflow); err != nil {
			return fmt.Errorf("decode realtime feed overflow: %w", err)
		}
	}
	return &FeedOverflowError{Cause: cause, Overflow: overflow}
}
