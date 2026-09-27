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

	currentDomain := ""
	for _, component := range components {
		if component.Domain != currentDomain {
			currentDomain = component.Domain
			presenter.Section(doctorDomainTitle(currentDomain))
		}
		presenter.Subsection(string(component.ID))
		presenter.NestedFields(
			presentation.Field{Label: "state", Value: component.State},
			presentation.Field{Label: "severity", Value: component.Severity},
			presentation.Field{Label: "summary", Value: component.Summary},
		)
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

	presenter.Section("Summary")
	presenter.Fields(
		presentation.Field{Label: "healthy", Value: report.Healthy},
		presentation.Field{Label: "warnings", Value: report.Warnings},
		presentation.Field{Label: "errors", Value: report.Errors},
		presentation.Field{Label: "provider failures", Value: report.ProviderFailures},
	)
	completion := "Diagnostics complete"
	if report.Healthy {
		completion = "Healthy"
	}
	commandProgressSession(cmd).SetCompletion(completion)
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
