package application

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"go.mewis.me/codemcp/internal/config"
	"go.mewis.me/codemcp/internal/runtime/control"
	"go.mewis.me/codemcp/internal/secretstore"
)

func TestSetupTelegramRollsBackTokenWhenAuthorizationReloadFails(t *testing.T) {
	root := isolateSettingServiceConfig(t)
	restoreSecrets := secretstore.UseMemoryForTesting()
	defer restoreSecrets()

	before, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if call == 2 {
			http.Error(w, "reload failed", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(control.ReloadResult{PID: os.Getpid()})
	}))
	defer server.Close()
	writeRuntimeState(t, root, control.State{
		PID: os.Getpid(), Address: strings.TrimPrefix(server.URL, "http://"), Token: "token", ConfigRoot: root,
	})

	_, err = SetupTelegram(t.Context(), TelegramSetupInput{Token: "123456:setup-token", UserID: 42})
	if err == nil || !errors.Is(err, ErrConfigMutationRolledBack) {
		t.Fatalf("setup err=%v", err)
	}
	if calls.Load() != 3 {
		t.Fatalf("reload calls=%d want=3", calls.Load())
	}
	if configured, tokenErr := telegramTokenConfigured(); tokenErr != nil || configured {
		t.Fatalf("telegram token configured=%t err=%v", configured, tokenErr)
	}
	after, loadErr := config.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("setup rollback changed config\nbefore=%#v\nafter=%#v", before, after)
	}
}
