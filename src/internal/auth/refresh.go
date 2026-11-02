package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"golang.org/x/oauth2"
)

// SessionUser renews a browser session near token expiry. An explicit bearer
// is never allowed to fall back to a cookie, including on /api/auth/me.
func (s *Service) SessionUser(w http.ResponseWriter, r *http.Request) (User, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if r == nil || s == nil || s.sessions == nil {
		return User{}, false
	}
	if user, ok := UserFromContext(r.Context()); ok {
		return user, true
	}
	if _, bearer, _ := parseBearerToken(r.Header.Get("Authorization")); bearer {
		return User{}, false
	}
	data, err := s.sessions.load(r)
	if err != nil || data.User == nil {
		return User{}, false
	}
	u := data.User
	now := s.now()
	if u.Subject == "" || (u.SessionExpiresAt != 0 && now.Unix() >= u.SessionExpiresAt) {
		s.sessions.clear(w)
		return User{}, false
	}
	if u.ExpiresAt <= now.Add(time.Minute).Unix() && u.RefreshToken != "" {
		// Older cookies without an absolute deadline cannot acquire an unbounded session.
		if u.SessionExpiresAt == 0 || s.oauthConfig == nil || s.refreshes == nil {
			s.sessions.clear(w)
			return User{}, false
		}
		key := u.RefreshToken + "\x00" + u.Subject + "\x00" + strconv.FormatInt(u.SessionExpiresAt, 10)
		refreshed, err := s.refreshes.load(r.Context(), key, s.now, func(ctx context.Context) (sessionUser, time.Time, error) {
			return s.refreshSession(ctx, *u)
		})
		if err != nil {
			s.sessions.clear(w)
			return User{}, false
		}
		data.User = &refreshed
		if err := s.sessions.save(w, data); err != nil {
			s.sessions.clear(w)
			return User{}, false
		}
		u = data.User
	}
	if u.ExpiresAt <= s.now().Unix() {
		s.sessions.clear(w)
		return User{}, false
	}
	return User{Subject: u.Subject, Email: u.Email, Name: u.Name, Picture: u.Picture, EmailVerified: u.EmailVerified}, true
}

func (s *Service) refreshSession(ctx context.Context, user sessionUser) (sessionUser, time.Time, error) {
	upstream := s.withOIDCClient(ctx)
	// Force a refresh even during the early renewal window.
	token, err := s.oauthConfig.TokenSource(upstream, &oauth2.Token{RefreshToken: user.RefreshToken, Expiry: time.Unix(1, 0)}).Token()
	if err != nil {
		return sessionUser{}, time.Time{}, fmt.Errorf("refresh oidc session: %w", err)
	}
	if token.AccessToken == "" || !token.Expiry.After(s.now()) {
		return sessionUser{}, time.Time{}, ErrInvalidToken
	}
	if raw, ok := token.Extra("id_token").(string); ok && raw != "" {
		if s.verifier == nil {
			return sessionUser{}, time.Time{}, ErrInvalidToken
		}
		verified, err := s.verifier.Verify(upstream, raw)
		if err != nil || verified.Subject != user.Subject {
			return sessionUser{}, time.Time{}, ErrInvalidToken
		}
		if verified.Nonce != "" && subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(user.AuthNonce)) != 1 {
			return sessionUser{}, time.Time{}, ErrInvalidNonce
		}
		var claims User
		if err := verified.Claims(&claims); err != nil {
			return sessionUser{}, time.Time{}, ErrInvalidToken
		}
		user.Email, user.Name, user.Picture, user.EmailVerified = claims.Email, claims.Name, claims.Picture, claims.EmailVerified
		user.IDToken = raw
		if verified.Expiry.Before(token.Expiry) {
			token.Expiry = verified.Expiry
		}
	}
	user.ExpiresAt = token.Expiry.Unix()
	if token.RefreshToken != "" {
		user.RefreshToken = token.RefreshToken
	}
	expires := s.now().Add(15 * time.Second)
	for _, deadline := range []time.Time{token.Expiry, time.Unix(user.SessionExpiresAt, 0)} {
		if deadline.Before(expires) {
			expires = deadline
		}
	}
	return user, expires, nil
}
