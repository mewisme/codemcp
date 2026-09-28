package admin

import (
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/application"
	"go.mewis.me/codemcp/internal/capability"
)

func (api API) handleRTK(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/integrations/rtk"), "/")
	switch {
	case r.Method == http.MethodGet && path == "":
		api.dispatch(w, r, capability.IntegrationRTKStatus, nil)
	case r.Method == http.MethodPost && path == "enable":
		api.dispatch(w, r, capability.IntegrationRTKEnable, nil)
	case r.Method == http.MethodPost && path == "disable":
		api.dispatch(w, r, capability.IntegrationRTKDisable, nil)
	case r.Method == http.MethodPost && path == "probe":
		api.dispatch(w, r, capability.IntegrationRTKProbe, nil)
	case r.Method == http.MethodPost && path == "install":
		api.dispatch(w, r, capability.IntegrationRTKInstall, nil)
	case r.Method == http.MethodGet && path == "global":
		api.dispatch(w, r, capability.IntegrationRTKInstallGlobal, nil)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleCodeGraph(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/integrations/codegraph"), "/")
	switch {
	case r.Method == http.MethodGet && path == "":
		api.dispatch(w, r, capability.IntegrationCodeGraphStatus, nil)
	case r.Method == http.MethodPost && path == "probe":
		api.dispatch(w, r, capability.IntegrationCodeGraphProbe, nil)
	case r.Method == http.MethodPost && path == "install":
		api.dispatch(w, r, capability.IntegrationCodeGraphInstall, nil)
	case r.Method == http.MethodGet && path == "global":
		api.dispatch(w, r, capability.IntegrationCodeGraphInstallGlobal, nil)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleCFIntegration(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/integrations/cf"), "/")
	switch {
	case r.Method == http.MethodGet && path == "":
		api.dispatch(w, r, capability.IntegrationCFStatus, nil)
	case r.Method == http.MethodDelete && path == "":
		api.dispatch(w, r, capability.IntegrationCFRemove, nil)
	case r.Method == http.MethodPost && path == "probe":
		api.dispatch(w, r, capability.IntegrationCFProbe, nil)
	case r.Method == http.MethodPost && path == "install":
		api.dispatch(w, r, capability.IntegrationCFInstall, nil)
	case r.Method == http.MethodPost && path == "update":
		api.dispatch(w, r, capability.IntegrationCFUpdate, nil)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (api API) handleTypeSafeToggle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/enable") {
		api.dispatch(w, r, capability.IntegrationTypeSafeEnable, nil)
		return
	}
	api.dispatch(w, r, capability.IntegrationTypeSafeDisable, nil)
}

func (api API) handleCodeGraphWorkspace(w http.ResponseWriter, r *http.Request, workspaceID string, parts []string) bool {
	if len(parts) < 2 || parts[0] != "integrations" || parts[1] != "codegraph" {
		return false
	}
	input := application.CodeGraphWorkspaceInput{WorkspaceID: workspaceID, Path: r.URL.Query().Get("path")}
	action := ""
	if len(parts) > 2 {
		action = parts[2]
	}
	switch {
	case r.Method == http.MethodGet && action == "":
		api.dispatch(w, r, capability.IntegrationCodeGraphWorkspaceStatus, input)
	case r.Method == http.MethodPost && action == "init":
		api.dispatch(w, r, capability.IntegrationCodeGraphWorkspaceInit, input)
	case r.Method == http.MethodPost && action == "sync":
		api.dispatch(w, r, capability.IntegrationCodeGraphWorkspaceSync, input)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
	return true
}
