package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.mewis.me/codemcp/internal/outboundpolicy"
)

func (s *Store) clientForTargets(trustedOrigins ...string) *http.Client {
	client := *s.client
	previous := client.CheckRedirect
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if err := validateOutboundURL(request.Context(), request.URL.String(), trustedOrigins...); err != nil {
			return fmt.Errorf("OAuth redirect denied: %w", err)
		}
		if previous != nil {
			return previous(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	return &client
}

func validateOutboundURL(ctx context.Context, raw string, trustedOrigins ...string) error {
	return outboundpolicy.ValidateURL(ctx, raw, outboundpolicy.Options{
		TrustedOrigins: append([]string(nil), trustedOrigins...),
	})
}
