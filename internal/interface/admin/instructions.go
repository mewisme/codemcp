package admin

import (
	"net/http"

	"go.mewis.me/codemcp/internal/application"
)

type instructionSettingsResponse = application.InstructionSettings
type instructionSettingsPatch = application.InstructionSettingsPatch

func (api API) handleGlobalInstructions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		value, err := application.LoadInstructionSettings()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, value)
	case http.MethodPut:
		var patch instructionSettingsPatch
		if err := decodeJSONBody(w, r, &patch); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		value, err := application.SaveInstructionSettings(patch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, value)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
