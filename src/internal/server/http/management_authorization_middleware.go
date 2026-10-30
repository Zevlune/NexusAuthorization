package httpserver

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/luck/nexus-authorization/internal/auth"
	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/logging"
)

// ManagementAuthorizationMiddleware 使用已认证用户和固定服务标识检查管理接口权限。
// appKey 仅满足现有鉴权接口的输入要求，Redis 策略按服务和用户合并。
func ManagementAuthorizationMiddleware(
	authorizer interface {
		Authorize(context.Context, string, map[string]any) (biz.RBACDecision, error)
	},
	appKey, serviceResource string,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := auth.UserFromContext(r.Context())
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="management"`)
				writeRawJSON(w, http.StatusUnauthorized, map[string]any{
					"success":      false,
					"errorCode":    "UNAUTHENTICATED",
					"errorMessage": "authentication required",
					"result":       nil,
				})
				return
			}
			if authorizer == nil {
				writeManagementAuthorizationUnavailable(w, r, nil)
				return
			}

			// 身份来自认证上下文，方法和路径来自当前请求，不能由调用者指定主体。
			decision, err := authorizer.Authorize(r.Context(), appKey, map[string]any{
				"subject":          user.Subject,
				"domain":           biz.NormalizeRBACDomain(""),
				"service_resource": serviceResource,
				"method":           r.Method,
				"path":             r.URL.Path,
			})
			if err != nil {
				writeManagementAuthorizationUnavailable(w, r, err)
				return
			}
			if !decision.Allowed {
				writeRawJSON(w, http.StatusForbidden, map[string]any{
					"success":      false,
					"errorCode":    "FORBIDDEN",
					"errorMessage": "permission denied",
					"result":       nil,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func writeManagementAuthorizationUnavailable(w http.ResponseWriter, r *http.Request, err error) {
	message := "authorization service unavailable"
	if err != nil {
		logging.FromContext(r.Context()).Error(
			"Management authorization failed.",
			slog.String(logging.Subcategory, "ManagementAuthorization"),
			slog.Any("Error", err),
		)
	}
	writeRawJSON(w, http.StatusServiceUnavailable, map[string]any{
		"success":      false,
		"errorCode":    "AUTHORIZATION_UNAVAILABLE",
		"errorMessage": message,
		"result":       nil,
	})
}
