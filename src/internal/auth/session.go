package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luck/nexus-authorization/internal/config"
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
	secret, err := resolveSessionSecret(cfg)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte("nexus-authorization/session-key/" + secret))
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

// resolveSessionSecret preserves the generated key across process restarts.
// An explicit secret remains supported for deployments managing their own keys.
func resolveSessionSecret(cfg config.OIDCConfig) (string, error) {
	if cfg.SessionSecret != "" {
		secret := strings.TrimSpace(cfg.SessionSecret)
		if len(secret) < 32 || strings.HasPrefix(secret, "replace-with-") {
			return "", errors.New("oidc session secret must be at least 32 characters")
		}
		return secret, nil
	}
	path := cfg.SessionSecretFile
	if path == "" {
		path = filepath.Join("data", "oidc-session.key")
	}
	readKey := func() (string, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		secret := strings.TrimSpace(string(data))
		decoded, err := hex.DecodeString(secret)
		if err != nil || len(decoded) != 32 {
			return "", fmt.Errorf("invalid persisted OIDC session key at %s", path)
		}
		return secret, nil
	}
	if secret, err := readKey(); err == nil {
		return secret, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read OIDC session key: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create OIDC session key directory: %w", err)
	}
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return "", fmt.Errorf("generate OIDC session key: %w", err)
	}
	// Publish a fully written file without replacing another instance's key.
	file, err := os.CreateTemp(dir, ".oidc-session-*")
	if err != nil {
		return "", fmt.Errorf("create OIDC session key file: %w", err)
	}
	defer os.Remove(file.Name())
	secret := hex.EncodeToString(key)
	_, writeErr := file.WriteString(secret + "\n")
	if writeErr == nil {
		writeErr = file.Sync()
	}
	closeErr := file.Close()
	if writeErr != nil {
		return "", fmt.Errorf("write OIDC session key: %w", writeErr)
	}
	if closeErr != nil {
		return "", fmt.Errorf("close OIDC session key: %w", closeErr)
	}
	if err := os.Link(file.Name(), path); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", fmt.Errorf("persist OIDC session key: %w", err)
		}
		// A concurrent instance has already published the shared key.
		return readKey()
	}
	return secret, nil
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
