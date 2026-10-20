package auth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"time"

	authv1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"github.com/q-controller/qcontroller/src/pkg/auth/signed"
)

const (
	sessionCookieName = "qcontroller_session"
	stateCookieName   = "qcontroller_oauth_state"
	defaultSessionTTL = 8 * time.Hour
	defaultLoginTTL   = 10 * time.Minute
)

type Authenticator struct {
	verifiers []*verifier          // every configured issuer; checks Bearer tokens
	providers map[string]*provider // subset of issuers with client_id; handles browser login
	session   signed.Cookie
	state     signed.Cookie
	returnTo  string
}

func New(ctx context.Context, cfg *settingsv1.AuthConfig, secret []byte) (*Authenticator, error) {
	if cfg == nil {
		return nil, errors.New("nil auth config")
	}
	if len(cfg.Issuers) == 0 {
		return nil, errors.New("auth.issuers must not be empty")
	}

	var external *url.URL
	if cfg.ExternalUrl != "" {
		var err error
		external, err = url.Parse(cfg.ExternalUrl)
		if err != nil || external.Scheme == "" || external.Host == "" {
			return nil, fmt.Errorf("invalid auth.external_url %q", cfg.ExternalUrl)
		}
	}

	seen := make(map[string]struct{}, len(cfg.Issuers))
	verifiers := make([]*verifier, 0, len(cfg.Issuers))
	providers := make(map[string]*provider)
	for _, i := range cfg.Issuers {
		if _, exists := seen[i.Name]; exists {
			return nil, fmt.Errorf("duplicate issuer name %q", i.Name)
		}
		seen[i.Name] = struct{}{}

		v, p, err := discover(ctx, i, external)
		if err != nil {
			return nil, err
		}
		verifiers = append(verifiers, v)
		if p != nil {
			providers[i.Name] = p
		}
	}

	var secure bool
	var returnTo string
	if external != nil {
		secure = external.Scheme == "https"
		returnTo = external.JoinPath("/ui/").String()
	}
	return &Authenticator{
		verifiers: verifiers,
		providers: providers,
		session: signed.Cookie{
			Name:   sessionCookieName,
			Path:   "/",
			TTL:    cmp.Or(time.Duration(cfg.SessionTtlSeconds)*time.Second, defaultSessionTTL),
			Key:    secret,
			Secure: secure,
		},
		state: signed.Cookie{
			Name:   stateCookieName,
			Path:   callbackPath,
			TTL:    cmp.Or(time.Duration(cfg.LoginTtlSeconds)*time.Second, defaultLoginTTL),
			Key:    secret,
			Secure: secure,
		},
		returnTo: returnTo,
	}, nil
}

// Providers returns the interactive login providers, sorted by name. Used by /v1/auth/config.
func (a *Authenticator) Providers() []*authv1.AuthProvider {
	out := make([]*authv1.AuthProvider, 0, len(a.providers))
	for name, p := range a.providers {
		out = append(out, &authv1.AuthProvider{
			Name:        name,
			DisplayName: p.displayName,
			LoginUrl:    loginPath + "?provider=" + url.QueryEscape(name),
		})
	}
	slices.SortFunc(out, func(a, b *authv1.AuthProvider) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return out
}
