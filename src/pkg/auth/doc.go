// Package auth implements OpenID Connect (OIDC) authentication for the
// orchestrator.
//
// An [Authenticator] is built once at startup from the configured OIDC issuers
// and the cookie-signing secret. Each issuer produces a token verifier (used
// for Authorization: Bearer tokens), and any issuer with a client_id also
// registers an interactive login provider (used for the browser OAuth2
// authorization-code flow).
//
// Three transport adapters consume an [Authenticator]:
//   - [Gate] ([NewGate]) wraps the HTTP mux to verify Bearer tokens or browser
//     session cookies, renew expired ID tokens, enforce authentication on
//     non-exempt paths, and check the CSRF header on mutating cookie requests.
//   - [RegisterLoginFlow] mounts the browser redirect/cookie endpoints
//     (/v1/auth/login, /v1/auth/callback, /v1/auth/logout).
//   - [AuthServer] ([NewAuthServer]) serves the gRPC-gateway discovery and
//     introspection RPCs (/v1/auth/config, /v1/auth/me).
package auth
