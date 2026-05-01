package auth

// PublicPaths returns the auth paths that must remain reachable without
// authentication: the login flow (HTTP-redirect) plus the JSON RPCs that
// either anyone can hit (config) or that operate on cookies whether
// authenticated or not (logout). Callers pass these (plus their own
// static-asset prefixes) into Middleware as exempt prefixes.
//
// /auth/me is intentionally NOT here — it's the only auth endpoint that
// requires a valid session.
func PublicPaths() []string {
	return []string{loginPath, callbackPath, logoutPath, configPath}
}
