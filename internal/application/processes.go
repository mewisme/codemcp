package application

import (
	"errors"

	shellruntime "go.mewis.me/codemcp/internal/runtime/shell"
)

var ErrProcessServiceUnavailable = errors.New("process manager unavailable")

type ProcessService struct {
	processes *shellruntime.ProcessManager
}

func NewProcessService(processes *shellruntime.ProcessManager) *ProcessService {
	return &ProcessService{processes: processes}
}

func (service *ProcessService) ClearFinished(workspaceID, id string) error {
	if service == nil || service.processes == nil {
		return ErrProcessServiceUnavailable
	}
	return service.processes.ClearFinished(workspaceID, id)
}
