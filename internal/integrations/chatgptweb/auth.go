package chatgptweb

import (
	"context"
	"errors"
	"strings"

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
	evidence.Account = sanitizeAccountSummary(evidence.Account)
	if !evidence.Authenticated {
		evidence.Account = AccountSummary{}
	}
	return evidence, nil
}

func sanitizeAccountSummary(account AccountSummary) AccountSummary {
	return AccountSummary{
		Name:  boundedAccountField(account.Name, 160),
		Email: boundedAccountField(account.Email, 320),
	}
}

func boundedAccountField(value string, maxRunes int) string {
	value = strings.TrimSpace(value)
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) > maxRunes {
		runes = runes[:maxRunes]
	}
	return string(runes)
}

const authEvidenceExpression = `/*codemcp:auth*/(async () => {
  const locationURL = new URL(window.location.href);
  const originOK = locationURL.origin === "https://chatgpt.com";
  const temporaryChat = locationURL.pathname === "/" && locationURL.searchParams.get("temporary-chat") === "true";
  let authenticated = false;
  let account = null;
  try {
    const response = await fetch("https://chatgpt.com/api/auth/session", {
      method: "GET",
      credentials: "include",
      cache: "no-store",
      redirect: "error",
      headers: { "accept": "application/json" }
    });
    const contentType = (response.headers.get("content-type") || "").toLowerCase();
    if (response.ok && contentType.includes("application/json")) {
      const payload = await response.json();
      const user = payload && payload.user;
      const userOK = Boolean(user && typeof user === "object" && !Array.isArray(user) && Object.keys(user).length > 0);
      const noError = Boolean(payload && !payload.error);
      let expiryOK = true;
      if (payload && payload.expires) {
        const expiresAt = Date.parse(payload.expires);
        expiryOK = Number.isFinite(expiresAt) && expiresAt > Date.now();
      }
      authenticated = userOK && noError && expiryOK;
      if (authenticated) {
        account = {
          name: typeof user.name === "string" ? user.name : "",
          email: typeof user.email === "string" ? user.email : ""
        };
      }
    }
  } catch (_) {}
  const composer = Boolean(document.querySelector('` + ComposerSelector + `'));
  return {
    origin_ok: originOK,
    temporary_chat: temporaryChat,
    authenticated,
    composer,
    account
  };
})()`
