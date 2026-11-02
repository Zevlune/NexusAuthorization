package repo

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/luck/nexus-authorization/internal/biz"
)

func TestOversizedEndpointFieldIsInvalidArgument(t *testing.T) {
	f := newPublicationFixture(t)
	_, err := f.repo.SaveEndpoint(context.Background(), f.service, biz.RBACEndpoint{
		Method: "GET", PathTemplate: "/oversized-module", Module: strings.Repeat("界", 201), Enabled: true,
	})
	if !errors.Is(err, biz.ErrInvalidArgument) {
		t.Fatalf("oversized module should map PostgreSQL 22001 to a client error: %v", err)
	}
	items, err := f.repo.ListEndpoints(context.Background(), f.service)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.PathTemplate == "/oversized-module" {
			t.Fatal("invalid endpoint was saved")
		}
	}
}
