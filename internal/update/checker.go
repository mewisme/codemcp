package update

import (
	"context"
	"strings"

	versionpkg "go.mewis.me/codemcp/internal/version"
)

type Status string

const (
	StatusAvailable   Status = "available"
	StatusUpToDate    Status = "up-to-date"
	StatusAhead       Status = "ahead"
	StatusDevelopment Status = "development"
)

type CheckResult struct {
	Current string
	Latest  string
	Status  Status
	Release Release
}

type ReleaseSource interface {
	Latest(context.Context) (Release, error)
}

type Checker struct {
	Source ReleaseSource
}

func (c Checker) Check(ctx context.Context, current string) (CheckResult, error) {
	source := c.Source
	if source == nil {
		source = Client{}
	}
	release, err := source.Latest(ctx)
	if err != nil {
		return CheckResult{}, err
	}
	return checkRelease(current, release)
}

func checkRelease(current string, release Release) (CheckResult, error) {
	current = strings.TrimSpace(current)
	if isDevelopmentVersion(current) {
		return CheckResult{Current: current, Latest: release.Version, Status: StatusDevelopment, Release: release}, nil
	}
	normalizedCurrent, err := NormalizeVersion(current)
	if err != nil {
		return CheckResult{}, err
	}
	comparison, err := CompareVersions(normalizedCurrent, release.Version)
	if err != nil {
		return CheckResult{}, err
	}
	status := StatusUpToDate
	if comparison < 0 {
		status = StatusAvailable
	} else if comparison > 0 {
		status = StatusAhead
	}
	return CheckResult{Current: normalizedCurrent, Latest: release.Version, Status: status, Release: release}, nil
}

func isDevelopmentVersion(version string) bool {
	return versionpkg.IsDevelopment(version)
}
