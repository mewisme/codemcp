package tools

import (
	"errors"
	"reflect"
	"testing"
)

func TestOAuthChallengeResultUsesProtocolMeta(t *testing.T) {
	result := OAuthChallengeResult(errors.New("authorization required"), "", `Bearer resource_metadata="https://example.test/.well-known/oauth-protected-resource/mcp"`)
	if !result.IsError {
		t.Fatal("challenge result must be an error")
	}
	want := []string{`Bearer resource_metadata="https://example.test/.well-known/oauth-protected-resource/mcp"`}
	if !reflect.DeepEqual(result.Meta["mcp/www_authenticate"], want) {
		t.Fatalf("challenge meta=%#v", result.Meta)
	}
}
