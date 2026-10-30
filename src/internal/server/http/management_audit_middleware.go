package httpserver

import (
	"log/slog"
	"net/http"

	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/logging"
)

type managementAuditWriter struct {
	http.ResponseWriter
	status int
}

func (w *managementAuditWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *managementAuditWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *managementAuditWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

// ManagementAuditMiddleware records authenticated management write attempts.
// Place it after authentication and before authorization to include denials.
// Bodies, credentials and arbitrary query parameters are never logged.
func ManagementAuditMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		user, authenticated := auth.UserFromContext(r.Context())
		response := &managementAuditWriter{ResponseWriter: w}
		completed := false
		defer func() {
			status := response.status
			if !completed {
				status = http.StatusInternalServerError
			} else if status == 0 {
				status = http.StatusOK
			}
			outcome := "failed"
			if status >= 200 && status < 300 {
				outcome = "succeeded"
			}
			logging.FromContext(r.Context()).Info("Management operation completed.",
				slog.String(logging.Subcategory, "ManagementAudit"),
				slog.String(logging.Filter2, user.Subject),
				slog.String("Actor", user.Subject),
				slog.Bool("Authenticated", authenticated),
				slog.String("Method", r.Method),
				slog.String("Path", r.URL.Path),
				slog.String("ApplicationKey", r.URL.Query().Get("app_key")),
				slog.String("ServiceResource", r.URL.Query().Get("service_resource")),
				slog.Int("StatusCode", status),
				slog.String("Outcome", outcome),
			)
		}()
		next.ServeHTTP(response, r)
		completed = true
	})
}
