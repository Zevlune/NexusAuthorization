package httpserver

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/luck/nexus-authorization/internal/biz"
	"github.com/luck/nexus-authorization/internal/logging"
)

// writeJSON 以统一成功包裹格式写入 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, value any) {
	writeRawJSON(w, status, map[string]any{
		"success":      true,
		"errorCode":    nil,
		"errorMessage": nil,
		"result":       value,
	})
}

// writeRawJSON 写入指定状态码和内容的原始 JSON 响应。
func writeRawJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// errUnsupportedMediaType rejects JSON bodies sent with another content type.
var errUnsupportedMediaType = errors.New("Content-Type must be application/json")

// writeError 将业务错误映射为统一的 HTTP 错误响应。
// Unexpected errors are logged with the request and answered with a generic
// message, so database or upstream details never reach the client.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	message := err.Error()
	switch {
	case errors.Is(err, errUnsupportedMediaType):
		status = http.StatusUnsupportedMediaType
	case errors.Is(err, biz.ErrInvalidArgument):
		status = http.StatusBadRequest
	case errors.Is(err, biz.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, biz.ErrAlreadyExists), errors.Is(err, biz.ErrConflict):
		status = http.StatusConflict
	case errors.Is(err, biz.ErrPublishPending):
		// The change is committed; only its Redis publication is pending.
		status = http.StatusServiceUnavailable
		message = "配置已保存，但授权记录发布失败，后台会自动重试；如持续失败请检查 Redis"
		logging.FromContext(r.Context()).Error("Publishing authorization records failed after save.", slog.Any("Error", err))
	default:
		message = "internal server error"
		logging.FromContext(r.Context()).Error("Request failed.", slog.Any("Error", err))
	}
	writeRawJSON(w, status, map[string]any{
		"success":      false,
		"errorCode":    apiErrorCode(err),
		"errorMessage": message,
		"result":       nil,
	})
}

// apiErrorCode 将业务错误映射为 API 错误码。
func apiErrorCode(err error) string {
	switch {
	case errors.Is(err, errUnsupportedMediaType):
		return "UNSUPPORTED_MEDIA_TYPE"
	case errors.Is(err, biz.ErrPublishPending):
		return "PUBLISH_PENDING"
	case errors.Is(err, biz.ErrInvalidArgument):
		return "INVALID_ARGUMENT"
	case errors.Is(err, biz.ErrNotFound):
		return "NOT_FOUND"
	case errors.Is(err, biz.ErrAlreadyExists):
		return "ALREADY_EXISTS"
	case errors.Is(err, biz.ErrConflict):
		return "CONFLICT"
	default:
		return "INTERNAL_ERROR"
	}
}
