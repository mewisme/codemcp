package tools

import (
	"context"
	"encoding/json"
	"errors"

	"go.mewis.me/codemcp/internal/integrations/codegraph"
	"go.mewis.me/codemcp/internal/projectcontext"
)

func codeGraphProjectContextProviders(runtime *Runtime) ProjectContextProviders {
	providers := ProjectContextProviders{Projections: []projectcontext.IntegrationProjectionProvider{
		codegraph.ProjectContextProjectionProvider(func() (*codegraph.Runtime, error) {
			return runtime.codeGraphRuntimeSnapshot(), nil
		}, runtime.Workspaces),
	}}
	if runtime != nil {
		providers.Semantic = runtime.Semantic
	}
	return providers
}

func codeGraphToolEntries(runtime *Runtime) map[string]Entry {
	return map[string]Entry{
		codegraph.ToolName: {
			Schema: Schema{
				Name:         codegraph.ToolName,
				Title:        "Explore CodeGraph",
				Description:  "Explore the indexed code graph for an authorized registered workspace project. The tool remains available while CodeGraph is disabled or unavailable and returns actionable state guidance instead of falling back to unrelated search.",
				InputSchema:  json.RawMessage(codegraph.ExploreInputSchema()),
				OutputSchema: json.RawMessage(codegraph.ExploreOutputSchema()),
				Annotations:  ToolAnnotations(RiskRead),
			},
			Handler: func(ctx context.Context, args map[string]any) (Result, error) {
				if runtime == nil {
					return Result{}, errors.New("tool runtime is unavailable")
				}
				workspaceID, err := requiredString(args, "workspace_id")
				if err != nil {
					return Result{}, err
				}
				query, err := requiredString(args, "query")
				if err != nil {
					return Result{}, err
				}
				projectPath, err := optionalString(args, "path")
				if err != nil {
					return Result{}, err
				}
				state, err := codegraph.Explore(ctx, runtime.codeGraphRuntimeSnapshot(), runtime.Workspaces, codegraph.ExploreInput{
					WorkspaceID: workspaceID,
					Query:       query,
					Path:        projectPath,
				})
				if err != nil {
					return Result{}, err
				}
				return JSONResult(state), nil
			},
		},
	}
}
