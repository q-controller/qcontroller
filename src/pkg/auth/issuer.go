package auth

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	authv1 "github.com/q-controller/qcontroller/src/generated/services/auth/v1"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	"golang.org/x/oauth2"
)

func discover(ctx context.Context, i *settingsv1.OIDCIssuer, external *url.URL) (*verifier, *provider, error) {
	switch {
	case i.Name == "":
		return nil, nil, errors.New("issuer.name must be set")
	case i.Audience == "" && i.ClientId == "":
		return nil, nil, fmt.Errorf("issuer %q needs audience or client_id", i.Name)
	case i.ClientId != "" && external == nil:
		return nil, nil, fmt.Errorf("issuer %q has client_id but auth.external_url is empty", i.Name)
	}

	oidcProvider, err := oidc.NewProvider(ctx, i.IssuerUrl)
	if err != nil {
		return nil, nil, fmt.Errorf("oidc discovery for %q: %w", i.Name, err)
	}

	v := newVerifier(oidcProvider, i)
	if i.ClientId == "" {
		return v, nil, nil
	}
	return v, newProvider(oidcProvider, i, v, external.JoinPath(callbackPath).String()), nil
}

type verifier struct {
	name string
	oidc *oidc.IDTokenVerifier
}

func newVerifier(oidcProvider *oidc.Provider, i *settingsv1.OIDCIssuer) *verifier {
	return &verifier{
		name: i.Name,
		oidc: oidcProvider.Verifier(&oidc.Config{ClientID: cmp.Or(i.Audience, i.ClientId)}),
	}
}

func (v *verifier) identify(ctx context.Context, rawIDToken string) (*authv1.Identity, time.Time, error) {
	tok, err := v.oidc.Verify(ctx, rawIDToken)
	if err != nil {
		return nil, time.Time{}, err
	}
	var claims struct {
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	if err := tok.Claims(&claims); err != nil {
		return nil, time.Time{}, err
	}
	return &authv1.Identity{
		Subject:  tok.Subject,
		Email:    claims.Email,
		Name:     claims.Name,
		Groups:   claims.Groups,
		IssuedBy: v.name,
	}, tok.Expiry, nil
}

type provider struct {
	*verifier
	displayName        string
	oauth2             oauth2.Config
	endSessionEndpoint string
}

func newProvider(oidcProvider *oidc.Provider, i *settingsv1.OIDCIssuer, v *verifier, redirectURL string) *provider {
	var disc struct {
		EndSessionEndpoint string `json:"end_session_endpoint"`
	}
	_ = oidcProvider.Claims(&disc) // best-effort; not all IdPs advertise it
	return &provider{
		verifier:    v,
		displayName: cmp.Or(i.DisplayName, i.Name),
		oauth2: oauth2.Config{
			ClientID:     i.ClientId,
			ClientSecret: i.ClientSecret,
			Endpoint:     oidcProvider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		endSessionEndpoint: disc.EndSessionEndpoint,
	}
}

func (p *provider) start(state string) string {
	return p.oauth2.AuthCodeURL(state)
}

func (p *provider) finish(ctx context.Context, code string) (session, error) {
	tok, err := p.oauth2.Exchange(ctx, code)
	if err != nil {
		return session{}, err
	}
	return p.session(ctx, tok, "")
}

func (p *provider) renew(ctx context.Context, refreshToken string) (session, error) {
	tok, err := p.oauth2.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken}).Token()
	if err != nil {
		return session{}, err
	}
	return p.session(ctx, tok, refreshToken)
}

func (p *provider) logoutURL(idToken, postLogoutRedirect string) string {
	if p.endSessionEndpoint == "" {
		return ""
	}
	params := url.Values{}
	if idToken != "" {
		params.Set("id_token_hint", idToken)
	}
	params.Set("client_id", p.oauth2.ClientID)
	if postLogoutRedirect != "" {
		params.Set("post_logout_redirect_uri", postLogoutRedirect)
	}
	return p.endSessionEndpoint + "?" + params.Encode()
}

func (p *provider) session(ctx context.Context, tok *oauth2.Token, prevRefreshToken string) (session, error) {
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		return session{}, errors.New("no id_token in token response")
	}
	id, exp, err := p.identify(ctx, rawID)
	if err != nil {
		return session{}, err
	}
	return session{
		Identity:     id,
		IDToken:      rawID,
		RefreshToken: cmp.Or(tok.RefreshToken, prevRefreshToken),
		Provider:     p.name,
		TokenExpiry:  exp,
	}, nil
}
