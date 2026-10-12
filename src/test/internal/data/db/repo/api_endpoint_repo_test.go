package repo

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/data/db/model"
)

func TestToBizAPIEndpointMapsFieldsAndAuditState(t *testing.T) {
	id := uuid.New()
	createdAt := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)
	value := toBizAPIEndpoint(&model.APIEndpoint{
		BaseFields:      model.BaseFields{CreatedByID: "creator", CreatedByName: "Creator", CreatedAt: createdAt, UpdatedByID: "editor", UpdatedByName: "Editor", UpdatedAt: updatedAt, IsDeleted: true},
		ID:              id,
		ServiceResource: "orders",
		Controller:      "orders",
		Method:          "GET",
		PathTemplate:    "/v1/orders",
		Summary:         "List orders",
		Enabled:         false,
	})
	if value == nil || value.ID != id || value.ServiceResource != "orders" || value.Controller != "orders" || value.Method != "GET" || value.PathTemplate != "/v1/orders" || value.Summary != "List orders" || value.Enabled || !value.IsDeleted {
		t.Fatalf("mapped endpoint = %#v", value)
	}
	if value.CreatedByID != "creator" || value.UpdatedByID != "editor" || !value.CreatedAt.Equal(createdAt) || !value.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("mapped audit fields = %#v", value.BaseFields)
	}
}
