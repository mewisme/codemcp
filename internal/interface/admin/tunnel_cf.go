package admin

import (
	"net/http"
	"strings"

	"go.mewis.me/codemcp/internal/capability"
)

func (api API) handleCFTunnel(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/tunnel/cf" && r.Method == http.MethodGet:
		api.dispatch(w, r, capability.TunnelCFStatus, nil)
	case path == "/api/tunnel/cf" && r.Method == http.MethodDelete:
		api.dispatch(w, r, capability.TunnelCFRemove, nil)
	case path == "/api/tunnel/cf/probe" && r.Method == http.MethodPost:
		api.dispatch(w, r, capability.TunnelCFProbe, nil)
	case path == "/api/tunnel/cf/install" && r.Method == http.MethodPost:
		api.dispatch(w, r, capability.TunnelCFInstall, nil)
	case path == "/api/tunnel/cf/update" && r.Method == http.MethodPost:
		api.dispatch(w, r, capability.TunnelCFUpdate, nil)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
