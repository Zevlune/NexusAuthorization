package repo

import (
	"errors"
	"testing"

	"github.com/luck/permission-center-go/internal/biz"
)

func TestNormalizeUserRoleScope(t *testing.T) {
	subject, serviceResource, err := normalizeUserRoleScope(" user-1 ", " admin ")
	if err != nil {
		t.Fatal(err)
	}
	if subject != "user-1" || serviceResource != "admin" {
		t.Fatalf("scope = %q/%q", subject, serviceResource)
	}
	if _, _, err := normalizeUserRoleScope("", "admin"); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("empty subject error = %v", err)
	}
	if _, _, err := normalizeUserRoleScope("user-1", " "); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("empty service resource error = %v", err)
	}
}

func TestNormalizeRoleIDs(t *testing.T) {
	roleIDs, err := normalizeRoleIDs([]string{" role-a ", "role-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(roleIDs) != 2 || roleIDs[0] != "role-a" || roleIDs[1] != "role-b" {
		t.Fatalf("role ids = %#v", roleIDs)
	}
	if _, err := normalizeRoleIDs([]string{"role-a", "role-a"}); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("duplicate role error = %v", err)
	}
	if _, err := normalizeRoleIDs([]string{""}); !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("empty role error = %v", err)
	}
}
