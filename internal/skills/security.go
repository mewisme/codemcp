package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"go.mewis.me/codemcp/internal/outboundpolicy"
)

const (
	DefaultSecurityAuditURL      = "https://add-skill.vercel.sh/audit"
	DefaultSecurityGitHubAPIURL  = "https://api.github.com"
	DefaultSecurityAuditTimeout  = 3 * time.Second
	maxSecurityAuditResponseSize = 1 << 20
)

type SecurityRisk string

const (
	SecurityRiskSafe     SecurityRisk = "safe"
	SecurityRiskLow      SecurityRisk = "low"
	SecurityRiskMedium   SecurityRisk = "medium"
	SecurityRiskHigh     SecurityRisk = "high"
	SecurityRiskCritical SecurityRisk = "critical"
	SecurityRiskUnknown  SecurityRisk = "unknown"
)

type PartnerAudit struct {
	Risk       SecurityRisk `json:"risk"`
	Alerts     *int         `json:"alerts,omitempty"`
	Score      *float64     `json:"score,omitempty"`
	AnalyzedAt string       `json:"analyzedAt,omitempty"`
}

type SkillSecurityAssessment struct {
	Name   string        `json:"name"`
	Gen    *PartnerAudit `json:"gen,omitempty"`
	Socket *PartnerAudit `json:"socket,omitempty"`
	Snyk   *PartnerAudit `json:"snyk,omitempty"`
}

type SecurityAssessment struct {
	Source     string                    `json:"source"`
	DetailsURL string                    `json:"details_url"`
	Skills     []SkillSecurityAssessment `json:"skills"`
}

func (assessment SecurityAssessment) HasData() bool {
	for _, skill := range assessment.Skills {
		if skill.Gen != nil || skill.Socket != nil || skill.Snyk != nil {
			return true
		}
	}
	return false
}

func (assessment SecurityAssessment) RequiresConfirmation() bool {
	for _, skill := range assessment.Skills {
		if auditRequiresConfirmation(skill.Gen) || auditRequiresConfirmation(skill.Snyk) {
			return true
		}
		if skill.Socket != nil && skill.Socket.Alerts != nil && *skill.Socket.Alerts > 0 {
			return true
		}
	}
	return false
}

func auditRequiresConfirmation(audit *PartnerAudit) bool {
	if audit == nil {
		return false
	}
	switch normalizeSecurityRisk(audit.Risk) {
	case SecurityRiskMedium, SecurityRiskHigh, SecurityRiskCritical:
		return true
	default:
		return false
	}
}

type SecurityAuditor struct {
	HTTPClient   *http.Client
	GitHubAPIURL string
	AuditURL     string
	Timeout      time.Duration
	LookupEnv    func(string) string
}

func DefaultSecurityAuditor() SecurityAuditor {
	return SecurityAuditor{
		HTTPClient:   outboundpolicy.NewHTTPClient(outboundpolicy.Options{}),
		GitHubAPIURL: DefaultSecurityGitHubAPIURL,
		AuditURL:     DefaultSecurityAuditURL,
		Timeout:      DefaultSecurityAuditTimeout,
		LookupEnv:    os.Getenv,
	}
}

func AuditPublicGitHubSkills(ctx context.Context, source GitHubSource, skillNames []string) (SecurityAssessment, error) {
	return DefaultSecurityAuditor().Audit(ctx, source, skillNames)
}

func (auditor SecurityAuditor) Audit(ctx context.Context, source GitHubSource, skillNames []string) (SecurityAssessment, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	lookupEnv := auditor.LookupEnv
	if lookupEnv == nil {
		lookupEnv = os.Getenv
	}
	if strings.TrimSpace(lookupEnv("DISABLE_TELEMETRY")) != "" || strings.TrimSpace(lookupEnv("DO_NOT_TRACK")) != "" {
		return SecurityAssessment{}, nil
	}
	names := normalizedSecuritySkillNames(skillNames)
	if len(names) == 0 {
		return SecurityAssessment{}, nil
	}
	client := auditor.HTTPClient
	if client == nil {
		client = outboundpolicy.NewHTTPClient(outboundpolicy.Options{})
	}
	timeout := auditor.Timeout
	if timeout <= 0 {
		timeout = DefaultSecurityAuditTimeout
	}
	githubAPI := strings.TrimRight(strings.TrimSpace(auditor.GitHubAPIURL), "/")
	if githubAPI == "" {
		githubAPI = DefaultSecurityGitHubAPIURL
	}
	auditURL := strings.TrimSpace(auditor.AuditURL)
	if auditURL == "" {
		auditURL = DefaultSecurityAuditURL
	}

	auditContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	public, err := confirmPublicGitHubRepository(auditContext, client, githubAPI, source)
	if err != nil || !public {
		return SecurityAssessment{}, err
	}

	parsed, err := url.Parse(auditURL)
	if err != nil {
		return SecurityAssessment{}, err
	}
	query := parsed.Query()
	query.Set("source", source.Owner+"/"+source.Repository)
	query.Set("skills", strings.Join(names, ","))
	parsed.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(auditContext, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return SecurityAssessment{}, err
	}
	request.Header.Set("User-Agent", "CodeMCP skill security audit")
	response, err := client.Do(request)
	if err != nil {
		return SecurityAssessment{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return SecurityAssessment{}, fmt.Errorf("skill security audit returned HTTP %d", response.StatusCode)
	}

	type providerMap map[string]PartnerAudit
	var payload map[string]providerMap
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxSecurityAuditResponseSize+1))
	if err := decoder.Decode(&payload); err != nil {
		return SecurityAssessment{}, err
	}
	assessment := SecurityAssessment{
		Source:     source.Owner + "/" + source.Repository,
		DetailsURL: "https://skills.sh/" + source.Owner + "/" + source.Repository,
		Skills:     make([]SkillSecurityAssessment, 0, len(names)),
	}
	for _, name := range names {
		providers := payload[name]
		value := SkillSecurityAssessment{Name: name}
		if audit, ok := providers["ath"]; ok {
			audit.Risk = normalizeSecurityRisk(audit.Risk)
			value.Gen = &audit
		}
		if audit, ok := providers["socket"]; ok {
			audit.Risk = normalizeSecurityRisk(audit.Risk)
			value.Socket = &audit
		}
		if audit, ok := providers["snyk"]; ok {
			audit.Risk = normalizeSecurityRisk(audit.Risk)
			value.Snyk = &audit
		}
		assessment.Skills = append(assessment.Skills, value)
	}
	if !assessment.HasData() {
		return SecurityAssessment{}, nil
	}
	return assessment, nil
}

func confirmPublicGitHubRepository(ctx context.Context, client *http.Client, apiBase string, source GitHubSource) (bool, error) {
	endpoint := apiBase + "/repos/" + url.PathEscape(source.Owner) + "/" + url.PathEscape(source.Repository)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "CodeMCP skill security audit")
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false, nil
	}
	var metadata struct {
		Private *bool `json:"private"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 64*1024)).Decode(&metadata); err != nil {
		return false, err
	}
	return metadata.Private != nil && !*metadata.Private, nil
}

func normalizedSecuritySkillNames(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeSecurityRisk(value SecurityRisk) SecurityRisk {
	switch SecurityRisk(strings.ToLower(strings.TrimSpace(string(value)))) {
	case SecurityRiskSafe:
		return SecurityRiskSafe
	case SecurityRiskLow:
		return SecurityRiskLow
	case SecurityRiskMedium:
		return SecurityRiskMedium
	case SecurityRiskHigh:
		return SecurityRiskHigh
	case SecurityRiskCritical:
		return SecurityRiskCritical
	default:
		return SecurityRiskUnknown
	}
}
