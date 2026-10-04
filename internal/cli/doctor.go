package cli

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/cli/presentation"
	"go.mewis.me/codemcp/internal/doctor"
)

var newDoctorService = func() (*application.DoctorService, error) {
	return application.NewDefaultDoctorService()
}

func doctorCommand() *cobra.Command {
	var outputJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Diagnose CodeMCP system health",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			service, err := newDoctorService()
			if err != nil {
				return err
			}
			result, err := service.Run(cmd.Context())
			if err != nil {
				return err
			}
			if commandResultModeFor(cmd) == resultModeJSON {
				return writeResultJSON(cmd, result.Value)
			}
			renderDoctorReport(cmd, result.Value)
			return nil
		},
	}
	addJSONResultFlag(cmd, &outputJSON)
	return cmd
}

func renderDoctorReport(cmd *cobra.Command, report doctor.Report) {
	presenter := commandPresenter(cmd)
	components := append([]doctor.Component(nil), report.Components...)
	sort.Slice(components, func(i, j int) bool {
		if components[i].Domain != components[j].Domain {
			return components[i].Domain < components[j].Domain
		}
		return components[i].ID < components[j].ID
	})

	if commandResultModeFor(cmd) == resultModeHuman {
		renderDoctorHumanReport(presenter, report, components)
		return
	}
	renderDoctorComponentsByDomain(presenter, components)
	renderDoctorSummary(presenter, report)
}

func renderDoctorHumanReport(presenter *presentation.Presenter, report doctor.Report, components []doctor.Component) {
	renderDoctorSummary(presenter, report)
	var issues, healthy []doctor.Component
	for _, component := range components {
		if component.Severity == doctor.SeverityWarning || component.Severity == doctor.SeverityError {
			issues = append(issues, component)
			continue
		}
		healthy = append(healthy, component)
	}
	if len(issues) > 0 {
		sort.SliceStable(issues, func(i, j int) bool {
			left, right := doctorSeverityRank(issues[i].Severity), doctorSeverityRank(issues[j].Severity)
			if left != right {
				return left < right
			}
			if issues[i].Domain != issues[j].Domain {
				return issues[i].Domain < issues[j].Domain
			}
			return issues[i].ID < issues[j].ID
		})
		presenter.Section("Needs attention")
		for _, component := range issues {
			renderDoctorIssue(presenter, component)
		}
	}
	if len(healthy) > 0 {
		presenter.Note("Healthy detail", "")
		renderDoctorComponentsByDomain(presenter, healthy)
	}
}

func renderDoctorSummary(presenter *presentation.Presenter, report doctor.Report) {
	presenter.Section("Summary")
	presenter.Fields(
		presentation.Field{Label: "healthy", Value: report.Healthy},
		presentation.Field{Label: "warnings", Value: report.Warnings},
		presentation.Field{Label: "errors", Value: report.Errors},
		presentation.Field{Label: "provider failures", Value: report.ProviderFailures},
	)
}

func renderDoctorComponentsByDomain(presenter *presentation.Presenter, components []doctor.Component) {
	for start := 0; start < len(components); {
		domain := components[start].Domain
		end := start + 1
		for end < len(components) && components[end].Domain == domain {
			end++
		}
		presenter.Section(doctorDomainTitle(domain))
		for index := start; index < end; index++ {
			renderDoctorComponentDetail(presenter, components[index], index == end-1)
		}
		start = end
	}
}

func renderDoctorIssue(presenter *presentation.Presenter, component doctor.Component) {
	presenter.ChildState(doctorPresentationKind(component), string(component.ID), component.State)
	presenter.NestedFields(
		presentation.Field{Label: "domain", Value: doctorDomainTitle(component.Domain)},
		presentation.Field{Label: "summary", Value: component.Summary},
	)
	renderDoctorComponentFacts(presenter, component)
}

func renderDoctorComponentDetail(presenter *presentation.Presenter, component doctor.Component, last bool) {
	presenter.SubsectionItem(string(component.ID), last)
	presenter.NestedFields(
		presentation.Field{Label: "state", Value: component.State},
		presentation.Field{Label: "severity", Value: component.Severity},
		presentation.Field{Label: "summary", Value: component.Summary},
	)
	renderDoctorComponentFacts(presenter, component)
}

func renderDoctorComponentFacts(presenter *presentation.Presenter, component doctor.Component) {
	metrics := append([]doctor.Metric(nil), component.Metrics...)
	sort.Slice(metrics, func(i, j int) bool { return metrics[i].ID < metrics[j].ID })
	for _, metric := range metrics {
		presenter.NestedFields(presentation.Field{Label: metric.ID, Value: metric.Value})
	}
	flags := append([]doctor.Flag(nil), component.Flags...)
	sort.Slice(flags, func(i, j int) bool { return flags[i].ID < flags[j].ID })
	for _, flag := range flags {
		presenter.NestedFields(presentation.Field{Label: flag.ID, Value: flag.Value})
	}
	remediations := append([]doctor.Remediation(nil), component.Remediations...)
	sort.Slice(remediations, func(i, j int) bool { return remediations[i].ID < remediations[j].ID })
	for _, remediation := range remediations {
		fields := []presentation.Field{{Label: "action", Value: remediation.Summary}}
		if strings.TrimSpace(remediation.Operation) != "" {
			fields = append(fields, presentation.Field{Label: "operation", Value: remediation.Operation})
		}
		presenter.NestedFields(fields...)
	}
}

func doctorSeverityRank(severity doctor.Severity) int {
	switch severity {
	case doctor.SeverityError:
		return 0
	case doctor.SeverityWarning:
		return 1
	default:
		return 2
	}
}

func doctorPresentationKind(component doctor.Component) presentation.StatusKind {
	switch component.Severity {
	case doctor.SeverityError:
		return presentation.StatusError
	case doctor.SeverityWarning:
		return presentation.StatusWarning
	}
	switch component.State {
	case doctor.StateHealthy:
		return presentation.StatusSuccess
	case doctor.StateDisabled:
		return presentation.StatusInactive
	default:
		return presentation.StatusInfo
	}
}

func doctorDomainTitle(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Other"
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' || r == '.' })
	for index := range parts {
		if parts[index] == "" {
			continue
		}
		parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
	}
	return strings.Join(parts, " ")
}
