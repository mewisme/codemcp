package chatgptweb

import (
	"context"
	"errors"

	"go.mewis.me/codemcp/internal/integrations/browser"
)

type AuthProbe interface {
	Probe(context.Context, browser.BrowserTab) (AuthEvidence, error)
}

type DOMAuthProbe struct{}

func (DOMAuthProbe) Probe(ctx context.Context, tab browser.BrowserTab) (AuthEvidence, error) {
	if tab == nil {
		return AuthEvidence{}, errors.New("browser tab is unavailable")
	}
	var evidence AuthEvidence
	if err := tab.Evaluate(ctx, authEvidenceExpression, &evidence); err != nil {
		return AuthEvidence{}, err
	}
	return evidence, nil
}

const authEvidenceExpression = `/*codemcp:auth*/(async () => {
  const locationURL = new URL(window.location.href);
  const originOK = locationURL.origin === "https://chatgpt.com";
  const temporaryChat = locationURL.pathname === "/" && locationURL.searchParams.get("temporary-chat") === "true";
  let authenticated = false;
  try {
    const response = await fetch("/api/auth/session", {
      method: "GET",
      credentials: "include",
      cache: "no-store",
      headers: { "accept": "application/json" }
    });
    if (response.ok) {
      const payload = await response.json();
      authenticated = Boolean(payload && payload.user);
    }
  } catch (_) {}
  const composer = Boolean(document.querySelector('` + ComposerSelector + `'));
  return {
    origin_ok: originOK,
    temporary_chat: temporaryChat,
    authenticated,
    composer
  };
})()`
