package completion

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"

	workspacestate "go.mewis.me/codemcp/internal/workspace/state"
)

var ErrWorkspaceMergeConflict = errors.New("completion history merge conflict")

func MergeWorkspaceState(registered, destination, output workspacestate.Store, workspaceID string) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return errors.New("completion workspace id is required")
	}
	registeredRecords, err := strictWorkspaceRecords(registered, workspaceID)
	if err != nil {
		return fmt.Errorf("registered completion history: %w", err)
	}
	destinationRecords, err := strictWorkspaceRecords(destination, workspaceID)
	if err != nil {
		return fmt.Errorf("destination completion history: %w", err)
	}
	merged := make(map[string]Record, len(registeredRecords)+len(destinationRecords))
	for _, group := range [][]Record{registeredRecords, destinationRecords} {
		for _, record := range group {
			if current, ok := merged[record.ID]; ok {
				if !reflect.DeepEqual(current, record) {
					return fmt.Errorf("%w: divergent record id %s", ErrWorkspaceMergeConflict, record.ID)
				}
				continue
			}
			merged[record.ID] = record
		}
	}
	records := make([]Record, 0, len(merged))
	for _, record := range merged {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].Sequence == records[j].Sequence {
			return records[i].ID < records[j].ID
		}
		return records[i].Sequence < records[j].Sequence
	})
	archiveCount := len(records) - DefaultMaxRecords
	if archiveCount < 0 {
		archiveCount = 0
	}
	if err := appendWorkspaceArchive(output, records[:archiveCount]); err != nil {
		return err
	}
	if err := saveWorkspaceHistory(output, workspaceHistory{Version: workspaceHistoryVersion, Records: append([]Record(nil), records[archiveCount:]...)}); err != nil {
		return err
	}
	validated, err := strictWorkspaceRecords(output, workspaceID)
	if err != nil {
		return fmt.Errorf("validate merged completion history: %w", err)
	}
	if len(validated) != len(records) {
		return fmt.Errorf("validate merged completion history: expected %d records, got %d", len(records), len(validated))
	}
	return nil
}

func strictWorkspaceRecords(local workspacestate.Store, workspaceID string) ([]Record, error) {
	history, _, err := loadWorkspaceHistory(local)
	if err != nil {
		return nil, err
	}
	archive, tailIssue, err := scanWorkspaceArchive(local)
	if err != nil {
		return nil, err
	}
	if tailIssue {
		return nil, errors.New("completion archive has an incomplete tail")
	}
	byID := map[string]Record{}
	for _, group := range [][]Record{archive, history.Records} {
		for _, record := range group {
			if strings.TrimSpace(record.ID) == "" || record.Sequence == 0 || record.WorkspaceID != workspaceID {
				return nil, errors.New("completion history contains invalid workspace-bound record")
			}
			switch record.Status {
			case StatusCompleted, StatusPartial, StatusBlocked, StatusCancelled:
			default:
				return nil, fmt.Errorf("completion history contains unsupported status %q", record.Status)
			}
			if strings.TrimSpace(record.AgentID) == "" || strings.TrimSpace(record.Title) == "" {
				return nil, errors.New("completion history contains incomplete record")
			}
			if current, ok := byID[record.ID]; ok {
				if !reflect.DeepEqual(current, record) {
					return nil, fmt.Errorf("%w: divergent record id %s", ErrWorkspaceMergeConflict, record.ID)
				}
				continue
			}
			byID[record.ID] = record
		}
	}
	result := make([]Record, 0, len(byID))
	for _, record := range byID {
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Sequence == result[j].Sequence {
			return result[i].ID < result[j].ID
		}
		return result[i].Sequence < result[j].Sequence
	})
	return result, nil
}
