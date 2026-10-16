package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/luck/nexus-authorization/internal/biz"
)

type fakeSCIMUserDirectory struct {
	filter     string
	startIndex int
	count      int
}

func (f *fakeSCIMUserDirectory) ListUsers(_ context.Context, filter string, startIndex, count int) (*biz.SCIMUserList, error) {
	f.filter, f.startIndex, f.count = filter, startIndex, count
	active := true
	return &biz.SCIMUserList{
		Schemas:      []string{"urn:ietf:params:scim:api:messages:2.0:ListResponse"},
		TotalResults: 1, StartIndex: startIndex, ItemsPerPage: 1,
		Resources: []biz.SCIMUser{{ID: "user-1", UserName: "alice", Active: &active}},
	}, nil
}

func TestListUsersCallsSCIMDirectory(t *testing.T) {
	directory := &fakeSCIMUserDirectory{}
	server := NewServer(NewHandler(nil).WithUserDirectory(directory))
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, `/api/v1/users?filter=userName%20eq%20%22alice%22&startIndex=2&count=20`, nil)
	server.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if directory.filter != `userName eq "alice"` || directory.startIndex != 2 || directory.count != 20 {
		t.Fatalf("directory request = %#v", directory)
	}
	var envelope struct {
		Success bool             `json:"success"`
		Result  biz.SCIMUserList `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if !envelope.Success || envelope.Result.TotalResults != 1 || len(envelope.Result.Resources) != 1 || envelope.Result.Resources[0].UserName != "alice" {
		t.Fatalf("response = %#v", envelope)
	}
}

func TestListUsersRejectsInvalidPagination(t *testing.T) {
	server := NewServer(NewHandler(nil).WithUserDirectory(&fakeSCIMUserDirectory{}))
	for _, query := range []string{"startIndex=0", "count=201", "count=abc", "count=1&count=2"} {
		response := httptest.NewRecorder()
		server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/users?"+query, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("query %q status = %d body=%s", query, response.Code, response.Body.String())
		}
	}
}

func TestListUsersReturnsNotConfigured(t *testing.T) {
	server := NewServer(NewHandler(nil))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/users", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
}
