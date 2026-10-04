package tools

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

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

type inlineApprovalRequest struct {
	ChallengeID string
	Title       string
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

func splitInlineApprovalArguments(args map[string]any) (map[string]any, *inlineApprovalRequest, error) {
	if args == nil {
		return map[string]any{}, nil, nil
	}
	raw, exists := args[InlineApprovalArgumentKey]
	if !exists {
		return args, nil, nil
	}
	business := cloneMap(args)
	delete(business, InlineApprovalArgumentKey)
	envelope, ok := raw.(map[string]any)
	if !ok {
		return business, nil, fmt.Errorf("%s must be an object", InlineApprovalArgumentKey)
	}
	if len(envelope) != 2 {
		return business, nil, fmt.Errorf("%s must contain exactly %s and %s", InlineApprovalArgumentKey, InlineApprovalChallengeID, InlineApprovalTitle)
	}
	challengeID, ok := envelope[InlineApprovalChallengeID].(string)
	challengeID = strings.TrimSpace(challengeID)
	if !ok || challengeID == "" {
		return business, nil, fmt.Errorf("%s.%s must be a non-empty string", InlineApprovalArgumentKey, InlineApprovalChallengeID)
	}
	title, ok := envelope[InlineApprovalTitle].(string)
	title = strings.TrimSpace(title)
	if !ok || title == "" {
		return business, nil, fmt.Errorf("%s.%s must be a non-empty string", InlineApprovalArgumentKey, InlineApprovalTitle)
	}
	if utf8.RuneCountInString(title) > InlineApprovalTitleMaxLen {
		return business, nil, fmt.Errorf("%s.%s must be at most %d characters", InlineApprovalArgumentKey, InlineApprovalTitle, InlineApprovalTitleMaxLen)
	}
	return business, &inlineApprovalRequest{ChallengeID: challengeID, Title: title}, nil
}

func supportsInlineApproval(schema Schema) bool {
	return schema.Approval != nil && schema.Approval.Inline
}
