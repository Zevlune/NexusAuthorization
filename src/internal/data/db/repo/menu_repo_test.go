package repo

import (
	"testing"

	"github.com/luck/permission-center-go/internal/biz"
)

func TestNullableMenuHTTPMethod(t *testing.T) {
	t.Run("empty method becomes null", func(t *testing.T) {
		if got := nullableMenuHTTPMethod(""); got != nil {
			t.Fatalf("nullableMenuHTTPMethod(\"\") = %#v, want nil", got)
		}
	})

	t.Run("configured method remains a string", func(t *testing.T) {
		if got := nullableMenuHTTPMethod(biz.MenuHTTPMethodPost); got != "POST" {
			t.Fatalf("nullableMenuHTTPMethod(POST) = %#v, want POST", got)
		}
	})
}
