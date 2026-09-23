package upstream

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"go.mewis.me/codemcp/internal/outboundpolicy"
)

type AuthConfig struct {
	Type  string `json:"type"`
	Scope string `json:"scope,omitempty"`
}

type Server struct {
	ID                  string            `json:"id"`
	Name                string            `json:"name"`
	Transport           string            `json:"transport"`
	Enabled             bool              `json:"enabled"`
	Command             string            `json:"command,omitempty"`
	Args                []string          `json:"args,omitempty"`
	Env                 map[string]string `json:"env,omitempty"`
	CWD                 string            `json:"cwd,omitempty"`
	URL                 string            `json:"url,omitempty"`
	Headers             map[string]string `json:"headers,omitempty"`
	BearerTokenEnvVar   string            `json:"bearer_token_env_var,omitempty"`
	Auth                AuthConfig        `json:"auth,omitempty"`
	ToolPrefix          string            `json:"tool_prefix,omitempty"`
	Expose              string            `json:"expose,omitempty"`
	Tools               []string          `json:"tools,omitempty"`
	DisabledTools       []string          `json:"disabled_tools,omitempty"`
	IdleTimeoutSec      int               `json:"idle_timeout_sec,omitempty"`
	AllowPrivateNetwork bool              `json:"allow_private_network,omitempty"`
}

func SensitiveConfigKey(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(lower, "authorization") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "api-key") || strings.Contains(lower, "cookie") || strings.HasSuffix(lower, "_key") || strings.HasSuffix(lower, "key")
}

func ParseAssignments(values []string, label string) (map[string]string, error) {
	result := map[string]string{}
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("%s must use KEY=VALUE: %s", label, value)
		}
		result[key] = item
	}
	return result, nil
}

func RedactServer(server Server) Server {
	value := server
	value.Headers = CloneStringMap(server.Headers)
	for key := range value.Headers {
		if SensitiveConfigKey(key) {
			value.Headers[key] = "<redacted>"
		}
	}
	value.Env = CloneStringMap(server.Env)
	for key := range value.Env {
		if SensitiveConfigKey(key) {
			value.Env[key] = "<redacted>"
		}
	}
	return value
}

func CloneStringMap(value map[string]string) map[string]string {
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

var invalidPrefix = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func NormalizeServer(value Server) (Server, error) {
	value.ID = strings.TrimSpace(value.ID)
	if value.ID == "" {
		return Server{}, errors.New("upstream server id is required")
	}
	value.Name = strings.TrimSpace(value.Name)
	if value.Name == "" {
		value.Name = value.ID
	}
	value.Transport = strings.ToLower(strings.TrimSpace(value.Transport))
	if value.Transport != "stdio" && value.Transport != "http" {
		return Server{}, errors.New("upstream transport must be stdio or http")
	}
	value.Command = strings.TrimSpace(value.Command)
	value.URL = strings.TrimSpace(value.URL)
	value.CWD = strings.TrimSpace(value.CWD)
	value.BearerTokenEnvVar = strings.TrimSpace(value.BearerTokenEnvVar)
	if value.Transport == "stdio" && value.Command == "" {
		return Server{}, errors.New("stdio upstream requires command")
	}
	if value.Transport == "http" && value.URL == "" {
		return Server{}, errors.New("http upstream requires url")
	}
	if value.Transport == "http" {
		if err := validateUpstreamURLConfig(value.URL, value.AllowPrivateNetwork); err != nil {
			return Server{}, err
		}
	}
	value.ToolPrefix = invalidPrefix.ReplaceAllString(strings.TrimSpace(value.ToolPrefix), "_")
	if value.ToolPrefix == "" {
		value.ToolPrefix = invalidPrefix.ReplaceAllString(value.ID, "_")
	}
	switch value.Expose {
	case "", "all":
		value.Expose = "all"
	case "none", "meta_only", "allowlist":
	default:
		return Server{}, errors.New("upstream expose must be none, meta_only, allowlist, or all")
	}
	if value.IdleTimeoutSec == 0 {
		value.IdleTimeoutSec = 600
	}
	if value.Auth.Type == "" {
		if value.Transport == "http" {
			value.Auth.Type = "auto"
		} else {
			value.Auth.Type = "none"
		}
	}
	switch value.Auth.Type {
	case "auto", "oauth", "none":
	default:
		return Server{}, errors.New("upstream auth type must be auto, oauth, or none")
	}
	if value.Args == nil {
		value.Args = []string{}
	}
	if value.Env == nil {
		value.Env = map[string]string{}
	}
	if value.Headers == nil {
		value.Headers = map[string]string{}
	}
	if err := validateConfiguredHeaders(value.Headers); err != nil {
		return Server{}, err
	}
	if value.Tools == nil {
		value.Tools = []string{}
	}
	if value.DisabledTools == nil {
		value.DisabledTools = []string{}
	}
	return value, nil
}

func validateUpstreamURLConfig(raw string, allowPrivate bool) error {
	parsed, err := outboundpolicy.ParseHTTPURL(raw)
	if err != nil {
		return err
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if parsed.Scheme != "https" && !loopback {
		return errors.New("upstream URL must use HTTPS unless the host is loopback")
	}
	if loopback && !allowPrivate {
		return errors.New("upstream URL targets loopback; set allow_private_network to permit")
	}
	if ip != nil && !allowPrivate && !outboundpolicy.IsPublicIP(ip) {
		return fmt.Errorf("upstream URL uses disallowed address %s; set allow_private_network to permit private or loopback targets", ip)
	}
	return nil
}

func validateConfiguredHeaders(headers map[string]string) error {
	for key, value := range headers {
		if err := validateConfiguredHeader(key, value); err != nil {
			return err
		}
	}
	return nil
}

func validateConfiguredHeader(name, value string) error {
	if name == "" {
		return errors.New("upstream header name is required")
	}
	if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("upstream header %q contains CR/LF", name)
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "host", "transfer-encoding", "connection", "keep-alive", "upgrade", "te", "trailer":
		return fmt.Errorf("upstream header %q is not allowed", name)
	}
	if strings.HasPrefix(lower, "proxy-") {
		return fmt.Errorf("upstream header %q is not allowed", name)
	}
	if !allowedConfiguredHeader(lower) {
		return fmt.Errorf("upstream header %q is not in the allowlist (Authorization, Accept, Content-Type, User-Agent, X-*, Mcp-*)", name)
	}
	return nil
}

func allowedConfiguredHeader(lower string) bool {
	switch lower {
	case "authorization", "accept", "content-type", "user-agent":
		return true
	}
	return strings.HasPrefix(lower, "x-") || strings.HasPrefix(lower, "mcp-")
}
