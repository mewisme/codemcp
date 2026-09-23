# MCP OAuth Fields

The OAuth login editor configures optional discovery/client overrides for an HTTP MCP server before starting authorization. All text fields are optional unless the remote OAuth flow itself requires the corresponding value.

## Issuer override

Overrides the issuer/discovery origin that would otherwise be inferred from server metadata. Leave blank to use the server-provided/default issuer behavior.

## Pre-registered client ID

Uses an already registered OAuth client identifier instead of relying only on dynamic/default client registration behavior. Leave blank when no explicit client ID is required.

## Client secret environment variable

Environment variable name containing the OAuth client secret. The secret value itself should not be entered into this field.

## Client metadata URL

Optional URL used as client metadata during OAuth registration/discovery where supported. Leave blank to use default client metadata behavior.

## Additional scopes

Additional scope string requested on top of the normal server/default authorization scopes.

## Open authorization URL in browser

When enabled, `cm` attempts to open the authorization URL in the user's browser as part of the login flow. Disabling it keeps the flow usable when browser launching is unavailable; the authorization URL can still be handled manually by the surrounding OAuth flow.

Submitting the editor starts authorization. Preflight or OAuth errors keep the editor and draft intact; successful authorization commits the draft before navigating away so the dirty guard does not ask to discard a completed login setup.
