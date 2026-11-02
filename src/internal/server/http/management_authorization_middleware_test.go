package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
)

type managementAuthorizerFunc func(context.Context, string, map[string]any) (biz.RBACDecision, error)

func (f managementAuthorizerFunc) Authorize(ctx context.Context, appKey string, input map[string]any) (biz.RBACDecision, error) {
	return f(ctx, appKey, input)
}

func TestManagementAuthorizationMiddlewareAllowsWithFixedAndRequestInput(t *testing.T) {
	var gotAppKey string
	var gotInput map[string]any
	authorizer := managementAuthorizerFunc(func(_ context.Context, appKey string, input map[string]any) (biz.RBACDecision, error) {
		gotAppKey = appKey
		gotInput = input
		return biz.RBACDecision{Allowed: true}, nil
	})
	nextCalled := false
	handler := ManagementAuthorizationMiddleware(authorizer, "permission-center", "management-api")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/rbac-v1/roles/role-1?domain=tenant-a", nil)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{Subject: "subject-1"}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent || !nextCalled {
		t.Fatalf("allowed request status = %d nextCalled=%v body=%s", response.Code, nextCalled, response.Body.String())
	}
	if gotAppKey != "permission-center" {
		t.Fatalf("appKey = %q, want permission-center", gotAppKey)
	}
	wantInput := map[string]any{
		"subject":          "subject-1",
		"domain":           biz.NormalizeRBACDomain(""),
		"service_resource": "management-api",
		"method":           http.MethodPatch,
		"path":             "/api/v1/rbac-v1/roles/role-1",
	}
	if !reflect.DeepEqual(gotInput, wantInput) {
		t.Fatalf("authorization input = %#v, want %#v", gotInput, wantInput)
	}
}

func TestManagementAuthorizationMiddlewareRequiresAuthenticatedIdentity(t *testing.T) {
	called := false
	authorizer := managementAuthorizerFunc(func(context.Context, string, map[string]any) (biz.RBACDecision, error) {
		called = true
		return biz.RBACDecision{Allowed: true}, nil
	})
	handler := ManagementAuthorizationMiddleware(authorizer, "permission-center", "management-api")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler must not be called without an identity")
	}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))

	body := decodeManagementAuthorizationResponse(t, response)
	if response.Code != http.StatusUnauthorized || called {
		t.Fatalf("missing identity status = %d authorizerCalled=%v body=%s", response.Code, called, response.Body.String())
	}
	if body["errorCode"] != "UNAUTHENTICATED" || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("missing identity response = %#v headers=%v", body, response.Header())
	}
}

func TestManagementAuthorizationMiddlewareForbidsDeniedDecision(t *testing.T) {
	authorizer := managementAuthorizerFunc(func(context.Context, string, map[string]any) (biz.RBACDecision, error) {
		return biz.RBACDecision{Allowed: false, Reason: "endpoint_not_granted"}, nil
	})
	handler := ManagementAuthorizationMiddleware(authorizer, "permission-center", "management-api")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler must not be called for a denied decision")
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{Subject: "subject-1"}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	body := decodeManagementAuthorizationResponse(t, response)
	if response.Code != http.StatusForbidden || body["errorCode"] != "FORBIDDEN" {
		t.Fatalf("denied request status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestManagementAuthorizationMiddlewareReturnsServiceUnavailableForDependencyErrors(t *testing.T) {
	authorizer := managementAuthorizerFunc(func(context.Context, string, map[string]any) (biz.RBACDecision, error) {
		return biz.RBACDecision{}, errors.New("redis password and internal address")
	})
	handler := ManagementAuthorizationMiddleware(authorizer, "permission-center", "management-api")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler must not be called after an authorization dependency error")
	}))
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/users/user-1", nil)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{Subject: "subject-1"}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	body := decodeManagementAuthorizationResponse(t, response)
	if response.Code != http.StatusServiceUnavailable || body["errorCode"] != "AUTHORIZATION_UNAVAILABLE" {
		t.Fatalf("dependency error status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "redis password") || strings.Contains(response.Body.String(), "internal address") {
		t.Fatalf("dependency error leaked: %s", response.Body.String())
	}
}

func TestManagementAuthorizationMiddlewareReturnsServiceUnavailableForMissingAuthorizer(t *testing.T) {
	handler := ManagementAuthorizationMiddleware(nil, "permission-center", "management-api")(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler must not be called without an authorizer")
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users", nil)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{Subject: "subject-1"}))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	body := decodeManagementAuthorizationResponse(t, response)
	if response.Code != http.StatusServiceUnavailable || body["errorCode"] != "AUTHORIZATION_UNAVAILABLE" {
		t.Fatalf("missing authorizer status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestManagementAuthorizationMiddlewareRunsAfterAuthenticationMiddleware(t *testing.T) {
	authorizer := managementAuthorizerFunc(func(_ context.Context, _ string, input map[string]any) (biz.RBACDecision, error) {
		if input["subject"] != "authenticated-subject" {
			return biz.RBACDecision{Allowed: false}, nil
		}
		return biz.RBACDecision{Allowed: true}, nil
	})
	authentication := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := auth.WithUser(r.Context(), auth.User{Subject: "authenticated-subject"})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	handler := authentication(ManagementAuthorizationMiddleware(authorizer, "permission-center", "management-api")(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	))
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("composed middleware status = %d body=%s", response.Code, response.Body.String())
	}
}

func decodeManagementAuthorizationResponse(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not JSON: %s", response.Body.String())
	}
	return body
}
