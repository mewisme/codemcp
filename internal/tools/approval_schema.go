package tools

const (
	InlineApprovalArgumentKey = "_approval"
	InlineApprovalChallengeID = "challenge_id"
	InlineApprovalTitle       = "title"
	InlineApprovalTitleMaxLen = 120
)

// ApprovalMetadata describes whether a tool schema should advertise the
// runtime-owned inline approval envelope. It is projection metadata only;
// runtime guards remain the approval authority.
type ApprovalMetadata struct {
	Inline bool `json:"inline"`
}

func inlineApprovalMetadata() *ApprovalMetadata {
	return &ApprovalMetadata{Inline: true}
}

func normalizeApprovalMetadata(metadata *ApprovalMetadata) (*ApprovalMetadata, error) {
	if metadata == nil {
		return nil, nil
	}
	if !metadata.Inline {
		return nil, errApprovalMetadata("inline must be true")
	}
	return &ApprovalMetadata{Inline: true}, nil
}

type approvalMetadataError string

func (err approvalMetadataError) Error() string {
	return "approval metadata " + string(err)
}

func errApprovalMetadata(detail string) error {
	return approvalMetadataError(detail)
}
