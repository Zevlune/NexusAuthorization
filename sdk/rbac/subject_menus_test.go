package rbac

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestSubjectMenusEscapesQueryAndBuildsTree(t *testing.T) {
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %s, want GET", request.Method)
		}
		if request.URL.Path != "/base/api/v1/subject-menus" {
			t.Fatalf("path = %q, want /base/api/v1/subject-menus", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer service secret" {
			t.Fatalf("authorization header = %q", request.Header.Get("Authorization"))
		}
		query := request.URL.Query()
		for name, want := range map[string]string{
			"app_key": "orders & billing",
			"subject": "user/one + two?",
			"domain":  "tenant a/b",
		} {
			if got := query.Get(name); got != want {
				t.Fatalf("%s = %q, want %q", name, got, want)
			}
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
				"success": true,
				"errorCode": null,
				"errorMessage": null,
				"result": {
					"items": [
						{"id":"child","application_id":"app","parent_id":"root","code":"create","name":"Create","menu_type":"button","sort_order":2,"enabled":true},
						{"id":"root","application_id":"app","parent_id":null,"code":"orders","name":"Orders","menu_type":"menu","sort_order":1,"enabled":true}
					]
				}
			}`)),
		}, nil
	})
	client := NewClient("https://nexus.example/base", "service secret")
	client.HTTP = &http.Client{Transport: transport}

	menus, err := client.SubjectMenus(context.Background(), "orders & billing", "user/one + two?", "tenant a/b")
	if err != nil {
		t.Fatalf("SubjectMenus returned error: %v", err)
	}
	if len(menus) != 1 || menus[0].ID != "root" || len(menus[0].Children) != 1 {
		t.Fatalf("menu tree = %+v", menus)
	}
	if child := menus[0].Children[0]; child.ID != "child" || child.MenuType != "button" || child.ParentID == nil || *child.ParentID != "root" {
		t.Fatalf("child = %+v", child)
	}
}

func TestSubjectMenusRejectsMissingCredentialWithoutRequest(t *testing.T) {
	called := false
	client := NewClient("https://nexus.example", "")
	client.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return nil, nil
	})}
	if _, err := client.SubjectMenus(context.Background(), "books", "user-1", ""); err == nil {
		t.Fatal("expected missing credential error")
	}
	if called {
		t.Fatal("HTTP request was sent without a credential")
	}
}

func TestSubjectMenusRejectsInvalidResponses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "non 200", status: http.StatusUnauthorized, body: `{"success":true,"result":{"items":[]}}`},
		{name: "malformed JSON", status: http.StatusOK, body: `{"success":true,"result":{"items":[`},
		{name: "trailing JSON", status: http.StatusOK, body: `{"success":true,"result":{"items":[]}} {}`},
		{name: "unsuccessful envelope", status: http.StatusOK, body: `{"success":false,"result":{"items":[]}}`},
		{name: "missing result", status: http.StatusOK, body: `{"success":true}`},
		{name: "missing items", status: http.StatusOK, body: `{"success":true,"result":{}}`},
		{name: "missing parent", status: http.StatusOK, body: `{"success":true,"result":{"items":[{"id":"child","parent_id":"missing"}]}}`},
		{name: "cycle", status: http.StatusOK, body: `{"success":true,"result":{"items":[{"id":"a","parent_id":"b"},{"id":"b","parent_id":"a"}]}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			transport := roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})
			client := NewClient("https://nexus.example", "secret")
			client.HTTP = &http.Client{Transport: transport}
			if _, err := client.SubjectMenus(context.Background(), "books", "user-1", ""); err == nil {
				t.Fatal("expected response validation error")
			}
		})
	}
}

func TestSubjectMenuTypeDecodesNestedChildren(t *testing.T) {
	var menu SubjectMenu
	if err := json.Unmarshal([]byte(`{"id":"root","children":[{"id":"child"}]}`), &menu); err != nil {
		t.Fatal(err)
	}
	if len(menu.Children) != 1 || menu.Children[0].ID != "child" {
		t.Fatalf("nested menu = %+v", menu)
	}
}
