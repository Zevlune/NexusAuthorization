package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/luck/nexus-authorization/internal/config"
)

func TestGeneratedSessionKeySurvivesRestart(t *testing.T) {
	cfg := config.OIDCConfig{SessionSecretFile: filepath.Join(t.TempDir(), "data", "oidc-session.key")}
	first, err := newSessionStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(cfg.SessionSecretFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key must be persisted with mode 0600: %v, %v", info, err)
	}
	response := httptest.NewRecorder()
	if err := first.save(response, &sessionData{User: &sessionUser{
		Subject: "user-1", ExpiresAt: time.Now().Add(time.Hour).Unix(),
	}}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(response.Result().Cookies()[0])
	restarted, err := newSessionStore(cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, err := restarted.load(request)
	if err != nil || data.User == nil || data.User.Subject != "user-1" {
		t.Fatalf("session lost after restart: %#v, %v", data, err)
	}
	other, err := newSessionStore(config.OIDCConfig{
		SessionSecretFile: filepath.Join(t.TempDir(), "oidc-session.key"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.load(request); !errors.Is(err, errInvalidSession) {
		t.Fatalf("independent deployment must reject cookie: %v", err)
	}
}

func TestConcurrentSessionKeyInitializationUsesOneKey(t *testing.T) {
	cfg := config.OIDCConfig{SessionSecretFile: filepath.Join(t.TempDir(), "data", "oidc-session.key")}
	const count = 16
	keys := make([]string, count)
	errs := make([]error, count)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := range keys {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			<-start
			keys[i], errs[i] = resolveSessionSecret(cfg)
		}(i)
	}
	close(start)
	workers.Wait()
	for i := range keys {
		if errs[i] != nil || keys[i] != keys[0] {
			t.Fatalf("instance %d did not load shared key: %v", i, errs[i])
		}
	}
	files, err := os.ReadDir(filepath.Dir(cfg.SessionSecretFile))
	if err != nil || len(files) != 1 || files[0].Name() != "oidc-session.key" {
		t.Fatalf("temporary key files were not cleaned up: %v, %v", files, err)
	}
}

func TestSessionKeyErrorsDoNotRegenerate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oidc-session.key")
	if err := os.WriteFile(path, []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newSessionStore(config.OIDCConfig{SessionSecretFile: path}); err == nil {
		t.Fatal("corrupted persisted key must fail startup")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "corrupted" {
		t.Fatal("corrupted key was overwritten")
	}
	if _, err := newSessionStore(config.OIDCConfig{SessionSecretFile: filepath.Join(path, "key")}); err == nil {
		t.Fatal("unwritable key location must fail startup")
	}
}

func TestExplicitSessionKeyDoesNotCreateFile(t *testing.T) {
	cfg := config.OIDCConfig{
		SessionSecret:     "test-session-secret-at-least-32-characters",
		SessionSecretFile: filepath.Join(t.TempDir(), "oidc-session.key"),
	}
	if _, err := newSessionStore(cfg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cfg.SessionSecretFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("explicit secret must bypass file generation: %v", err)
	}
}
