package logging

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestHandlerUsesCanonicalTemplateAndDefaults(t *testing.T) {
	var output bytes.Buffer
	logger := NewText(&output, "permission-center", "debug")
	record := slog.NewRecord(time.Date(2026, time.September, 9, 10, 11, 12, 123456000, time.Local), slog.LevelInfo, "Started", 0)
	record.Add(slog.String(Category, "HTTP"), slog.String("Address", ":8080"))
	if err := logger.Handler().Handle(context.Background(), record); err != nil {
		t.Fatal(err)
	}
	want := "[2026-09-09 10:11:12.123][INF][permission-center][HTTP][][][][][Started Address=:8080]\n"
	if output.String() != want {
		t.Fatalf("log output = %q, want %q", output.String(), want)
	}
}

func TestHandlerFiltersMinimumLevelAndEscapesNewlines(t *testing.T) {
	var output bytes.Buffer
	logger := NewText(&output, "module", "warning")
	logger.Info("ignored")
	logger.Warn("first\nsecond")
	if strings.Contains(output.String(), "ignored") || !strings.Contains(output.String(), "[WRN][module][][][][][][first\\nsecond]") {
		t.Fatalf("unexpected log output: %q", output.String())
	}
}

func TestHTTPMiddlewareAddsRequestContextAndCompletionLog(t *testing.T) {
	var output bytes.Buffer
	logger := NewText(&output, "permission-center", "info")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /items/{id}", func(w http.ResponseWriter, r *http.Request) {
		FromContext(r.Context()).Info("Inside handler.", slog.String(Subcategory, "GetItem"))
		w.WriteHeader(http.StatusAccepted)
	})
	server := HTTPMiddleware(logger, func(*http.Request) string { return "user-1" })(mux)
	request := httptest.NewRequest(http.MethodGet, "/items/42", nil)
	request.Header.Set(traceHeader, "trace-1")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Header().Get(traceHeader) != "trace-1" {
		t.Fatalf("response request id = %q", response.Header().Get(traceHeader))
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d: %q", len(lines), output.String())
	}
	if !strings.Contains(lines[0], "[HTTP][GET /items/{id}][GetItem][trace-1]") || !strings.Contains(lines[0], "[user-1][Inside handler.]") {
		t.Fatalf("handler log = %q", lines[0])
	}
	if !strings.Contains(lines[1], "[HTTP][GET /items/{id}][][trace-1]") || !strings.Contains(lines[1], "StatusCode=202") {
		t.Fatalf("completion log = %q", lines[1])
	}
}

func TestHTTPMiddlewareGeneratesRequestID(t *testing.T) {
	var output bytes.Buffer
	server := HTTPMiddleware(NewText(&output, "module", "info"), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	requestID := response.Header().Get(traceHeader)
	if matched, _ := regexp.MatchString(`^[0-9a-f]{32}$`, requestID); !matched {
		t.Fatalf("generated request id = %q", requestID)
	}
}

func TestHTTPMiddlewareLogsAndRethrowsPanic(t *testing.T) {
	var output bytes.Buffer
	server := HTTPMiddleware(NewText(&output, "module", "info"), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("failed")
	}))
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("expected panic")
			}
		}()
		server.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/panic", nil))
	}()
	if !strings.Contains(output.String(), "][ERR][HTTP][/panic]") ||
		!strings.Contains(output.String(), "Request failed. StatusCode=500") {
		t.Fatalf("panic log = %q", output.String())
	}
}
