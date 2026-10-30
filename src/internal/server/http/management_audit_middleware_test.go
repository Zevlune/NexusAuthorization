package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/logging"
)

func TestManagementAuditRecordsIdentityScopeAndOutcome(t *testing.T) {
	for _, tc := range []struct {
		name, outcome string
		allowed       bool
		status        int
	}{
		{"saved", "succeeded", true, http.StatusCreated},
		{"denied", "failed", false, http.StatusForbidden},
		{"handler-failed", "failed", true, http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&output, nil)).With(logging.RequestTraceID, "trace-123")
			r := httptest.NewRequest(http.MethodPost, "/api/v1/rbac-v1/roles?app_key=orders&service_resource=orders-api&token=query-secret", strings.NewReader(`{"secret":"body-secret"}`))
			r.Header.Set("Authorization", "Bearer header-secret")
			ctx := logging.WithLogger(auth.WithUser(r.Context(), auth.User{Subject: "alice"}), logger)
			r = r.WithContext(ctx)
			calls := 0
			authorizer := managementAuthorizerFunc(func(context.Context, string, map[string]any) (biz.RBACDecision, error) {
				return biz.RBACDecision{Allowed: tc.allowed}, nil
			})
			handler := ManagementAuditMiddleware(ManagementAuthorizationMiddleware(authorizer, "management", "management-api")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls++
				w.WriteHeader(tc.status)
			})))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, r)
			if response.Code != tc.status || (!tc.allowed && calls != 0) {
				t.Fatalf("status=%d calls=%d", response.Code, calls)
			}
			var event map[string]any
			if err := json.Unmarshal(output.Bytes(), &event); err != nil {
				t.Fatalf("audit log: %v: %s", err, output.String())
			}
			for key, want := range map[string]any{
				logging.Subcategory: "ManagementAudit", logging.RequestTraceID: "trace-123",
				"Actor": "alice", "Authenticated": true, "Method": http.MethodPost,
				"Path": "/api/v1/rbac-v1/roles", "ApplicationKey": "orders",
				"ServiceResource": "orders-api", "StatusCode": float64(tc.status), "Outcome": tc.outcome,
			} {
				if event[key] != want {
					t.Errorf("%s=%v, want %v", key, event[key], want)
				}
			}
			for _, secret := range []string{"query-secret", "header-secret", "body-secret"} {
				if strings.Contains(output.String(), secret) {
					t.Errorf("audit exposed %s", secret)
				}
			}
		})
	}
}

func TestManagementAuditHandlesImplicitSuccessAndPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		var output bytes.Buffer
		logger := slog.New(slog.NewJSONHandler(&output, nil))
		r := httptest.NewRequest(http.MethodDelete, "/api/v1/rbac-v1/roles/role-1", nil)
		r = r.WithContext(logging.WithLogger(r.Context(), logger))
		handler := ManagementAuditMiddleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			if panics {
				panic("handler failed")
			}
		}))
		func() {
			defer func() {
				if got := recover(); (got != nil) != panics {
					t.Errorf("panic propagation=%v", got)
				}
			}()
			handler.ServeHTTP(httptest.NewRecorder(), r)
		}()
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		wantStatus := float64(http.StatusOK)
		if panics {
			wantStatus = http.StatusInternalServerError
		}
		if event["StatusCode"] != wantStatus || event["Authenticated"] != false {
			t.Fatalf("unexpected audit event: %v", event)
		}
	}
}

func TestManagementAuditSkipsReads(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, nil))
	r := httptest.NewRequest(http.MethodGet, "/api/v1/applications", nil)
	r = r.WithContext(logging.WithLogger(r.Context(), logger))
	ManagementAuditMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(httptest.NewRecorder(), r)
	if output.Len() != 0 {
		t.Fatalf("read generated management mutation audit: %s", output.String())
	}
}
