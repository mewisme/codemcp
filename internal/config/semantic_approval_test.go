package config

import "testing"

func TestSemanticApprovalDefaultsAndFieldMutation(t *testing.T) {
	cfg := Default()
	cfg.Auth.MCPTokenHash = "test-mcp-hash"
	cfg.Auth.AdminTokenHash = "test-admin-hash"
	got := cfg.Approval.Semantic
	if got.Enabled || got.Provider != "typesafe" || got.TimeoutMS != 1500 || got.MinimumConfidence != 0.8 ||
		got.FailMode != "require_approval" || got.LowAction != "allow" || got.MediumAction != "require_approval" ||
		got.HighAction != "require_approval" || got.CriticalAction != "deny" {
		t.Fatalf("semantic approval defaults=%#v", got)
	}
	changes := map[string]string{
		"approval.semantic.enabled":            "true",
		"approval.semantic.provider":           "native-risk",
		"approval.semantic.timeout_ms":         "900",
		"approval.semantic.minimum_confidence": "0.91",
		"approval.semantic.fail_mode":          "deny",
		"approval.semantic.low_action":         "require_approval",
		"approval.semantic.medium_action":      "deny",
		"approval.semantic.high_action":        "deny",
		"approval.semantic.critical_action":    "deny",
	}
	for key, value := range changes {
		if err := SetValue(&cfg, key, value); err != nil {
			t.Fatalf("%s=%q: %v", key, value, err)
		}
	}
	if err := Validate(cfg); err != nil {
		t.Fatal(err)
	}
	for key, want := range changes {
		got, err := RawValue(cfg, key)
		if err != nil || got != want {
			t.Fatalf("%s=%q want=%q err=%v", key, got, want, err)
		}
	}
}

func TestSemanticApprovalRejectsUnsafeFailureAndMalformedPolicy(t *testing.T) {
	cfg := Default()
	if err := SetValue(&cfg, "approval.semantic.fail_mode", "allow"); err == nil {
		t.Fatal("semantic approval accepted fail-open allow mode")
	}
	if err := SetValue(&cfg, "approval.semantic.minimum_confidence", "1.1"); err == nil {
		t.Fatal("semantic approval accepted out-of-range confidence")
	}
	if err := SetValue(&cfg, "approval.semantic.timeout_ms", "10"); err == nil {
		t.Fatal("semantic approval accepted unbounded-small timeout")
	}
	cfg = Default()
	cfg.Auth.MCPTokenHash = "test-mcp-hash"
	cfg.Auth.AdminTokenHash = "test-admin-hash"
	cfg.Approval.Semantic.LowAction = "execute"
	if err := Validate(cfg); err == nil {
		t.Fatal("semantic approval accepted unknown risk action")
	}
}
