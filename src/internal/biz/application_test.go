package biz

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateApplicationURL(t *testing.T) {
	for _, value := range []string{"", "https://example.com", "http://localhost:5274/path?q=1#tab", "https://example.com/应用"} {
		t.Run("valid_"+value, func(t *testing.T) {
			if err := validateApplicationURL(value); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, value := range []string{"javascript:alert(1)", "data:text/html,test", "//example.com", "/app", "https://", "https://user:password@example.com", "https://example.com\\evil", "https://example.com/a b", "https://example.com/\n", "https://example.com/" + strings.Repeat("a", 2048)} {
		t.Run("invalid_"+value, func(t *testing.T) {
			if err := validateApplicationURL(value); !errors.Is(err, ErrInvalidArgument) {
				t.Fatalf("expected invalid URL, got %v", err)
			}
		})
	}
}
