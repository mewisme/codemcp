package instructioncontext

import "go.mewis.me/codemcp/internal/sequence"

const (
	changeRecentLimit      = 64
	changeSubscriberBuffer = 32
)

type Change struct {
	Sequence    uint64 `json:"sequence"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope"`
	WorkspaceID string `json:"workspace_id,omitempty"`
	Name        string `json:"name"`
	Operation   string `json:"operation"`
}

type ChangeSubscription = sequence.Subscription[Change]
type ChangeSnapshot = sequence.Snapshot[Change]

type ChangeStream struct {
	stream *sequence.Stream[Change]
}

func NewChangeStream() *ChangeStream {
	return newChangeStream(changeRecentLimit, changeSubscriberBuffer)
}

func newChangeStream(maxRecent, subscriberBuffer int) *ChangeStream {
	return &ChangeStream{stream: sequence.New[Change](maxRecent, subscriberBuffer, func(change *Change, value uint64) {
		change.Sequence = value
	})}
}

func (s *ChangeStream) Publish(change Change) Change {
	if s == nil || s.stream == nil {
		return change
	}
	return s.stream.Publish(change)
}

func (s *ChangeStream) Subscribe(recentLimit int) (*ChangeSubscription, ChangeSnapshot) {
	if s == nil || s.stream == nil {
		return nil, ChangeSnapshot{}
	}
	return s.stream.Subscribe(nil, recentLimit)
}

func (s *ChangeStream) Unsubscribe(subscription *ChangeSubscription) {
	if s != nil && s.stream != nil {
		s.stream.Unsubscribe(subscription)
	}
}

func (s *ChangeStream) AcknowledgeOverflow(subscription *ChangeSubscription) {
	if s != nil && s.stream != nil {
		s.stream.AcknowledgeOverflow(subscription)
	}
}
