package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSecurityAssessmentConfirmationThresholds(t *testing.T) {
	alerts := 1
	for _, test := range []struct {
		name string
		risk SecurityRisk
		want bool
	}{
		{name: "safe", risk: SecurityRiskSafe},
		{name: "low", risk: SecurityRiskLow},
		{name: "medium", risk: SecurityRiskMedium, want: true},
		{name: "high", risk: SecurityRiskHigh, want: true},
		{name: "critical", risk: SecurityRiskCritical, want: true},
		{name: "unknown", risk: SecurityRiskUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			assessment := SecurityAssessment{Skills: []SkillSecurityAssessment{{Name: "demo", Gen: &PartnerAudit{Risk: test.risk}}}}
			if got := assessment.RequiresConfirmation(); got != test.want {
				t.Fatalf("RequiresConfirmation()=%v want=%v", got, test.want)
			}
		})
	}
	if !((SecurityAssessment{Skills: []SkillSecurityAssessment{{Socket: &PartnerAudit{Alerts: &alerts}}}}).RequiresConfirmation()) {
		t.Fatal("socket alerts should require confirmation")
	}
}

func TestSecurityAuditorOnlySendsPublicRepositoryMetadata(t *testing.T) {
	var auditRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/repos/"):
			_ = json.NewEncoder(writer).Encode(map[string]any{"private": false})
		case request.URL.Path == "/audit":
			auditRequests.Add(1)
			if request.URL.Query().Get("source") != "owner/repo" || request.URL.Query().Get("skills") != "alpha,beta" {
				t.Fatalf("unexpected audit query: %s", request.URL.RawQuery)
			}
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"alpha": map[string]any{
					"ath":    map[string]any{"risk": "medium", "analyzedAt": "now"},
					"socket": map[string]any{"risk": "unknown", "alerts": 2, "analyzedAt": "now"},
					"snyk":   map[string]any{"risk": "safe", "analyzedAt": "now"},
				},
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	auditor := SecurityAuditor{
		HTTPClient: server.Client(), GitHubAPIURL: server.URL, AuditURL: server.URL + "/audit",
		LookupEnv: func(string) string { return "" },
	}
	assessment, err := auditor.Audit(context.Background(), GitHubSource{Owner: "owner", Repository: "repo"}, []string{"beta", "alpha", "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if auditRequests.Load() != 1 || !assessment.HasData() || !assessment.RequiresConfirmation() {
		t.Fatalf("assessment=%#v requests=%d", assessment, auditRequests.Load())
	}
	if len(assessment.Skills) != 2 || assessment.Skills[0].Name != "alpha" || assessment.Skills[1].Name != "beta" {
		t.Fatalf("skill ordering=%#v", assessment.Skills)
	}
}

func TestSecurityAuditorSkipsPrivateUnknownAndDoNotTrack(t *testing.T) {
	for _, test := range []struct {
		name       string
		private    any
		status     int
		doNotTrack string
	}{
		{name: "private", private: true, status: http.StatusOK},
		{name: "unknown", status: http.StatusNotFound},
		{name: "do-not-track", private: false, status: http.StatusOK, doNotTrack: "1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var auditRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/audit" {
					auditRequests.Add(1)
					_ = json.NewEncoder(writer).Encode(map[string]any{"demo": map[string]any{"ath": map[string]any{"risk": "critical"}}})
					return
				}
				writer.WriteHeader(test.status)
				if test.status == http.StatusOK {
					_ = json.NewEncoder(writer).Encode(map[string]any{"private": test.private})
				}
			}))
			defer server.Close()
			auditor := SecurityAuditor{
				HTTPClient: server.Client(), GitHubAPIURL: server.URL, AuditURL: server.URL + "/audit",
				LookupEnv: func(key string) string {
					if key == "DO_NOT_TRACK" {
						return test.doNotTrack
					}
					return ""
				},
			}
			assessment, err := auditor.Audit(context.Background(), GitHubSource{Owner: "owner", Repository: "repo"}, []string{"demo"})
			if err != nil {
				t.Fatal(err)
			}
			if assessment.HasData() || auditRequests.Load() != 0 {
				t.Fatalf("assessment=%#v auditRequests=%d", assessment, auditRequests.Load())
			}
		})
	}
}
