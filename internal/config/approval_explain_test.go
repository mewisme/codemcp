package config

import "testing"

func TestApprovalExplainModeDefaultsAndFieldMutation(t *testing.T) {
	cfg := Default()
	if cfg.Explain.Mode != ExplainOff {
		t.Fatalf("default mode=%q", cfg.Explain.Mode)
	}
	for _, mode := range []ApprovalExplainMode{ApprovalExplainManual, ApprovalExplainAuto, ApprovalExplainOff} {
		if err := SetValue(&cfg, "approval.explain.mode", string(mode)); err != nil {
			t.Fatalf("set %s: %v", mode, err)
		}
		got, err := RawValue(cfg, "approval.explain.mode")
		if err != nil || got != string(mode) {
			t.Fatalf("mode=%q want=%q err=%v", got, mode, err)
		}
	}
}

func TestApprovalExplainModeRejectsUnknownValue(t *testing.T) {
	cfg := Default()
	if err := SetValue(&cfg, "approval.explain.mode", "sometimes"); err == nil {
		t.Fatal("unknown Explain mode accepted")
	}
	cfg.Explain.Mode = "sometimes"
	if err := Validate(cfg); err == nil {
		t.Fatal("invalid persisted Explain mode accepted")
	}
}
