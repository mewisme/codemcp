package upstream

import (
	"errors"
	"fmt"
	"strings"
)

type HTTPStatusError struct {
	StatusCode      int
	Status          string
	WWWAuthenticate []string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return "upstream HTTP error"
	}
	return fmt.Sprintf("upstream HTTP %s", e.Status)
}

type OAuthLoginRequiredError struct {
	ServerID string
	Cause    error
}

func (e *OAuthLoginRequiredError) Error() string {
	message := fmt.Sprintf("upstream OAuth authorization required for %s; run: cm mcp server auth login %s", e.ServerID, e.ServerID)
	var status *HTTPStatusError
	if errors.As(e.Cause, &status) && len(status.WWWAuthenticate) > 0 {
		message += "; challenge: " + strings.Join(status.WWWAuthenticate, ", ")
	}
	return message
}

func (e *OAuthLoginRequiredError) Unwrap() error { return e.Cause }

func isOAuthHTTPChallenge(err error) bool {
	var status *HTTPStatusError
	return errors.As(err, &status) && (status.StatusCode == 401 || status.StatusCode == 403)
}
