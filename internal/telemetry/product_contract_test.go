package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"go.mewis.me/codemcp/internal/configformat"
)

// backendAuditCommit is the mewisme/telemetry revision re-verified when the
// product telemetry privacy contract was frozen. Backend changes must never
// silently widen CodeMCP's outbound field allowlist.
const backendAuditCommit = "4dc7043d73dfffe621861e517b3164e4ae4b9100"

type productTelemetryContract struct {
	Backend struct {
		Repository     string `json:"repository"`
		Commit         string `json:"commit"`
		HealthRoute    string `json:"health_route"`
		RouteTemplate  string `json:"route_template"`
		Product        string `json:"product"`
		Route          string `json:"route"`
		Schema         int    `json:"schema"`
		AcceptedStatus int    `json:"accepted_status"`
		AcceptedField  string `json:"accepted_field"`
		Limits         struct {
			MaxBodyBytes    int    `json:"max_body_bytes"`
			MaxBatchEvents  int    `json:"max_batch_events"`
			MaxStringLength int    `json:"max_string_length"`
			ProductPattern  string `json:"product_pattern"`
			EventPattern    string `json:"event_pattern"`
		} `json:"limits"`
	} `json:"backend"`
	Privacy struct {
		AllowedEventFields   []string `json:"allowed_event_fields"`
		ForbiddenEventFields []string `json:"forbidden_event_fields"`
		ErrorCodes           []string `json:"error_codes"`
	} `json:"privacy"`
	Configuration struct {
		Key              string   `json:"key"`
		DefaultEnabled   bool     `json:"default_enabled"`
		Env              string   `json:"env"`
		Precedence       []string `json:"precedence"`
		EnableValues     []string `json:"enable_values"`
		DisableValues    []string `json:"disable_values"`
		InvalidEnv       string   `json:"invalid_env"`
		MCPAgentVisible  bool     `json:"mcp_agent_visible"`
		MCPAgentWritable bool     `json:"mcp_agent_writable"`
	} `json:"configuration"`
	Identity struct {
		StatePath                          string `json:"state_path"`
		Schema                             int    `json:"schema"`
		Field                              string `json:"field"`
		Generation                         string `json:"generation"`
		DerivedFromMachineOrWorkspace      bool   `json:"derived_from_machine_or_workspace"`
		CreateWhenDisabled                 bool   `json:"create_when_disabled"`
		CreateWithoutEndpoint              bool   `json:"create_without_endpoint"`
		StatusCreatesIdentity              bool   `json:"status_creates_identity"`
		ShowCreatesIdentity                bool   `json:"show_creates_identity"`
		DisableDeletesIdentity             bool   `json:"disable_deletes_identity"`
		UninitRemovesIdentity              bool   `json:"uninit_removes_identity"`
		MalformedStateFailsParentOperation bool   `json:"malformed_state_fails_parent_operation"`
	} `json:"identity"`
	Transport struct {
		ExactlyOnce              bool `json:"exactly_once"`
		IdempotencyKey           bool `json:"idempotency_key"`
		AttemptsPerDequeuedBatch int  `json:"attempts_per_dequeued_batch"`
		RetryAmbiguousSend       bool `json:"retry_ambiguous_send"`
		PersistentSpool          bool `json:"persistent_spool"`
		DisabledNetwork          bool `json:"disabled_network"`
		EndpointlessNetwork      bool `json:"endpointless_network"`
		StatusNetwork            bool `json:"status_network"`
		ShowNetwork              bool `json:"show_network"`
	} `json:"transport"`
}

func TestProductTelemetryBackendAndPrivacyContract(t *testing.T) {
	t.Setenv(configformat.EnvConfigDir, t.TempDir())
	contract := loadProductTelemetryContract(t)

	if contract.Backend.Repository != "mewisme/telemetry" || contract.Backend.Commit != backendAuditCommit {
		t.Fatalf("backend audit drifted: %#v", contract.Backend)
	}
	if contract.Backend.Product != "codemcp" || contract.Backend.Schema != 1 || contract.Backend.Route != "/v1/products/codemcp/events" {
		t.Fatalf("product route/schema drifted: %#v", contract.Backend)
	}
	if contract.Backend.AcceptedStatus != 202 || contract.Backend.AcceptedField != "accepted" {
		t.Fatalf("accepted response contract drifted: %#v", contract.Backend)
	}
	if contract.Backend.Limits.MaxBodyBytes != 64*1024 || contract.Backend.Limits.MaxBatchEvents != 100 || contract.Backend.Limits.MaxStringLength != 128 {
		t.Fatalf("backend limits drifted: %#v", contract.Backend.Limits)
	}
	if _, err := regexp.Compile(contract.Backend.Limits.ProductPattern); err != nil {
		t.Fatalf("invalid product pattern: %v", err)
	}
	if _, err := regexp.Compile(contract.Backend.Limits.EventPattern); err != nil {
		t.Fatalf("invalid event pattern: %v", err)
	}

	wantAllowed := []string{"name", "anonymous_id", "version", "os", "arch", "interface", "command", "feature", "error_code", "duration_ms", "success"}
	if !reflect.DeepEqual(contract.Privacy.AllowedEventFields, wantAllowed) {
		t.Fatalf("outbound allowlist drifted\nwant=%v\ngot=%v", wantAllowed, contract.Privacy.AllowedEventFields)
	}
	for _, forbidden := range contract.Privacy.ForbiddenEventFields {
		if slices.Contains(contract.Privacy.AllowedEventFields, forbidden) {
			t.Fatalf("forbidden field %q entered outbound allowlist", forbidden)
		}
	}
	for _, field := range contract.Privacy.AllowedEventFields {
		if strings.Contains(field, "raw") || strings.Contains(field, "payload") || strings.Contains(field, "metadata") {
			t.Fatalf("arbitrary/raw field %q is not allowed", field)
		}
	}
}

func TestProductTelemetryConfigIdentityAndTransportContract(t *testing.T) {
	root := t.TempDir()
	t.Setenv(configformat.EnvConfigDir, root)
	contract := loadProductTelemetryContract(t)

	if contract.Configuration.Key != "telemetry.enabled" || !contract.Configuration.DefaultEnabled || contract.Configuration.Env != "CM_TELEMETRY" {
		t.Fatalf("configuration contract drifted: %#v", contract.Configuration)
	}
	if !reflect.DeepEqual(contract.Configuration.Precedence, []string{"env", "config", "default"}) {
		t.Fatalf("precedence=%v", contract.Configuration.Precedence)
	}
	if !reflect.DeepEqual(contract.Configuration.EnableValues, []string{"1", "true", "yes", "on"}) ||
		!reflect.DeepEqual(contract.Configuration.DisableValues, []string{"0", "false", "no", "off"}) {
		t.Fatalf("environment boolean contract drifted: %#v", contract.Configuration)
	}
	if contract.Configuration.InvalidEnv != "ignore" || contract.Configuration.MCPAgentVisible || contract.Configuration.MCPAgentWritable {
		t.Fatalf("operator-only privacy preference drifted: %#v", contract.Configuration)
	}

	if contract.Identity.StatePath != "state/product-telemetry.json" || contract.Identity.Schema != 1 ||
		contract.Identity.Field != "anonymous_id" || contract.Identity.Generation != "random_uuid" {
		t.Fatalf("identity persistence drifted: %#v", contract.Identity)
	}
	if contract.Identity.DerivedFromMachineOrWorkspace || contract.Identity.CreateWhenDisabled || contract.Identity.CreateWithoutEndpoint ||
		contract.Identity.StatusCreatesIdentity || contract.Identity.ShowCreatesIdentity || contract.Identity.DisableDeletesIdentity ||
		!contract.Identity.UninitRemovesIdentity || contract.Identity.MalformedStateFailsParentOperation {
		t.Fatalf("identity lifecycle drifted: %#v", contract.Identity)
	}

	if contract.Transport.ExactlyOnce || contract.Transport.IdempotencyKey || contract.Transport.AttemptsPerDequeuedBatch != 1 ||
		contract.Transport.RetryAmbiguousSend || contract.Transport.PersistentSpool || contract.Transport.DisabledNetwork ||
		contract.Transport.EndpointlessNetwork || contract.Transport.StatusNetwork || contract.Transport.ShowNetwork {
		t.Fatalf("transport safety contract drifted: %#v", contract.Transport)
	}

	statePath := filepath.Join(root, filepath.FromSlash(contract.Identity.StatePath))
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("contract/status fixture unexpectedly created identity state: %v", err)
	}
}

func loadProductTelemetryContract(t *testing.T) productTelemetryContract {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve telemetry contract test path")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(current), "testdata", "product-telemetry-contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract productTelemetryContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	return contract
}
