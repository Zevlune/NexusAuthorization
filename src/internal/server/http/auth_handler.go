package httpserver

import (
	"errors"
	"net/http"
	"strings"

	"github.com/luck/permission-center-go/internal/auth"
)

// AuthConfig 返回前端启动登录流程所需的认证开关。
func (h *Handler) AuthConfig(w http.ResponseWriter, _ *http.Request) {
	if h.auth == nil {
		writeJSON(w, http.StatusOK, auth.PublicConfig{})
		return
	}
	writeJSON(w, http.StatusOK, h.auth.PublicConfig())
}

// AuthLogin 创建 OIDC 登录请求并返回身份平台授权地址。
func (h *Handler) AuthLogin(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeAuthError(w, auth.ErrDisabled)
		return
	}
	authorizeURL, err := h.auth.Login(w, r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"authorizeUrl": authorizeURL})
}

// AuthCallback 处理 OIDC 回调、建立本地会话并跳转到管理端。
func (h *Handler) AuthCallback(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeAuthError(w, auth.ErrDisabled)
		return
	}
	if err := h.auth.Callback(w, r); err != nil {
		writeAuthError(w, err)
		return
	}
	// Follow the NexusAuth Workbench BFF pattern: the backend owns the OIDC
	// callback, then returns the browser to the frontend once its session exists.
	http.Redirect(w, r, dashboardCallbackURL(h.auth), http.StatusFound)
}

// dashboardCallbackURL 根据认证配置生成前端回调地址。
func dashboardCallbackURL(authenticator *auth.Service) string {
	if authenticator == nil {
		return "/auth/callback"
	}
	frontendBase := strings.TrimRight(authenticator.PublicConfig().PostLogoutRedirectURI, "/")
	if frontendBase == "" {
		return "/auth/callback"
	}
	return frontendBase + "/auth/callback"
}

// AuthMe 返回当前会话中的登录用户信息。
func (h *Handler) AuthMe(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeAuthError(w, auth.ErrUnauthenticated)
		return
	}
	if !h.auth.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{
			"isAuthenticated": true,
			"user":            map[string]string{"sub": "development", "name": "开发模式"},
		})
		return
	}
	user, ok := h.auth.Me(r)
	if !ok {
		writeAuthError(w, auth.ErrUnauthenticated)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"isAuthenticated": true, "user": user})
}

// AuthLogout 清除本地会话并返回身份平台退出地址。
func (h *Handler) AuthLogout(w http.ResponseWriter, r *http.Request) {
	if h.auth == nil {
		writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": ""})
		return
	}
	logoutURL, err := h.auth.Logout(w, r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"logoutUrl": logoutURL})
}

// writeAuthError 将认证错误写入统一的 HTTP 错误响应。
func writeAuthError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		status = http.StatusUnauthorized
	case errors.Is(err, auth.ErrDisabled):
		status = http.StatusServiceUnavailable
	case errors.Is(err, auth.ErrInvalidRequest), errors.Is(err, auth.ErrInvalidState), errors.Is(err, auth.ErrInvalidNonce):
		status = http.StatusBadRequest
	}
	writeRawJSON(w, status, map[string]any{
		"success":      false,
		"errorCode":    authErrorCode(err),
		"errorMessage": err.Error(),
		"result":       nil,
	})
}

// authErrorCode 将认证错误映射为 API 错误码。
func authErrorCode(err error) string {
	switch {
	case errors.Is(err, auth.ErrUnauthenticated):
		return "UNAUTHENTICATED"
	case errors.Is(err, auth.ErrDisabled):
		return "AUTH_DISABLED"
	case errors.Is(err, auth.ErrInvalidRequest):
		return "INVALID_REQUEST"
	case errors.Is(err, auth.ErrInvalidState):
		return "INVALID_STATE"
	case errors.Is(err, auth.ErrInvalidNonce):
		return "INVALID_NONCE"
	default:
		return "AUTH_ERROR"
	}
}
