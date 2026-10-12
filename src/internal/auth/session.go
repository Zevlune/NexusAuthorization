package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/luck/permission-center-go/internal/config"
)

const (
	defaultCookieName = "nexusauthorization_session"
	maxCookieAge      = 8 * time.Hour
	flowMaxAge        = 10 * time.Minute
	maxCookieBytes    = 3800
)

var errInvalidSession = errors.New("invalid authentication session")

type sessionUser struct {
	Subject       string `json:"sub"`
	Email         string `json:"email,omitempty"`
	Name          string `json:"name,omitempty"`
	Picture       string `json:"picture,omitempty"`
	EmailVerified bool   `json:"email_verified,omitempty"`
	ExpiresAt     int64  `json:"expires_at"`
	// IDToken is never returned by the API. It is retained in the encrypted
	// HttpOnly session only so NexusAuth can identify the browser session at logout.
	IDToken string `json:"id_token,omitempty"`
}

type sessionData struct {
	State         string       `json:"state,omitempty"`
	Nonce         string       `json:"nonce,omitempty"`
	PKCEVerifier  string       `json:"pkce_verifier,omitempty"`
	FlowCreatedAt int64        `json:"flow_created_at,omitempty"`
	User          *sessionUser `json:"user,omitempty"`
}

type sessionStore struct {
	name   string
	secure bool
	block  cipher.AEAD
}

func newSessionStore(cfg config.OIDCConfig) (*sessionStore, error) {
	secret := strings.TrimSpace(cfg.SessionSecret)
	if secret == "" {
		return nil, errors.New("oidc session secret is empty")
	}
	key := sha256.Sum256([]byte("permission-center/session-key/" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create session cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create session AEAD: %w", err)
	}
	name := strings.TrimSpace(cfg.CookieName)
	if name == "" {
		name = defaultCookieName
	}
	return &sessionStore{name: name, secure: cfg.CookieSecure, block: gcm}, nil
}

func (s *sessionStore) load(r *http.Request) (*sessionData, error) {
	if s == nil || s.block == nil {
		return &sessionData{}, nil
	}
	cookie, err := r.Cookie(s.name)
	if errors.Is(err, http.ErrNoCookie) {
		return &sessionData{}, nil
	}
	if err != nil {
		return nil, errInvalidSession
	}
	encoded, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(encoded) < s.block.NonceSize() {
		return nil, errInvalidSession
	}
	plaintext, err := s.block.Open(nil, encoded[:s.block.NonceSize()], encoded[s.block.NonceSize():], nil)
	if err != nil {
		return nil, errInvalidSession
	}
	var data sessionData
	if err := json.Unmarshal(plaintext, &data); err != nil {
		return nil, errInvalidSession
	}
	if data.FlowCreatedAt != 0 && time.Since(time.Unix(data.FlowCreatedAt, 0)) > flowMaxAge {
		data.State, data.Nonce, data.PKCEVerifier, data.FlowCreatedAt = "", "", "", 0
	}
	return &data, nil
}

func (s *sessionStore) save(w http.ResponseWriter, data *sessionData) error {
	if s == nil || s.block == nil {
		return nil
	}
	plaintext, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode authentication session: %w", err)
	}
	nonce := make([]byte, s.block.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return fmt.Errorf("generate session nonce: %w", err)
	}
	encoded := append(nonce, s.block.Seal(nil, nonce, plaintext, nil)...)
	value := base64.RawURLEncoding.EncodeToString(encoded)
	if len(value) > maxCookieBytes {
		return errors.New("authentication session exceeds cookie size limit")
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.name,
		Value:    value,
		Path:     "/",
		MaxAge:   int(maxCookieAge / time.Second),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *sessionStore) clear(w http.ResponseWriter) {
	if s == nil {
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     s.name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}
