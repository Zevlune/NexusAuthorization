package logging

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

const traceHeader = "X-Request-ID"

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// HTTPMiddleware adds the standard request fields and records request completion.
func HTTPMiddleware(logger *slog.Logger, resolveUserID func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			category := r.URL.Path
			if router, ok := next.(interface {
				Handler(*http.Request) (http.Handler, string)
			}); ok {
				if _, pattern := router.Handler(r); pattern != "" {
					category = pattern
				}
			}
			traceID := r.Header.Get(traceHeader)
			if strings.TrimSpace(traceID) == "" || len(traceID) > 256 || strings.ContainsAny(traceID, "\r\n") {
				traceID = newID()
			}
			r.Header.Set(traceHeader, traceID)
			filter1 := newID()
			filter2 := ""
			if resolveUserID != nil {
				filter2 = resolveUserID(r)
			}
			if filter2 == "" {
				filter2 = newID()
			}
			requestLogger := logger.With(
				slog.String(Module, "HTTP"),
				slog.String(Category, category),
				slog.String(RequestTraceID, traceID),
				slog.String(Filter1, filter1),
				slog.String(Filter2, filter2),
			)
			w.Header().Set(traceHeader, traceID)
			response := &statusWriter{ResponseWriter: w}
			defer func() {
				status := response.status
				if status == 0 {
					status = http.StatusOK
				}
				attributes := []any{
					slog.Int("StatusCode", status),
					slog.Int64("ElapsedMs", time.Since(started).Milliseconds()),
					slog.String("Method", r.Method),
					slog.String("Path", r.URL.Path),
				}
				if resolveUserID != nil {
					if userID := resolveUserID(r); userID != "" {
						attributes = append([]any{slog.String(Filter2, userID)}, attributes...)
					}
				}
				if recovered := recover(); recovered != nil {
					if response.status < http.StatusBadRequest {
						attributes[0] = slog.Int("StatusCode", http.StatusInternalServerError)
					}
					requestLogger.Error("Request failed.", append(attributes, slog.Any("Error", recovered))...)
					panic(recovered)
				}
				requestLogger.Info("Request completed.", attributes...)
			}()
			r = r.WithContext(WithLogger(r.Context(), requestLogger))
			next.ServeHTTP(response, r)
		})
	}
}

func newID() string { return strings.ReplaceAll(uuid.NewString(), "-", "") }
