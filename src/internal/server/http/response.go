package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/luck/permission-center-go/internal/biz"
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

// writeError 将业务错误映射为统一的 HTTP 错误响应。
func writeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, biz.ErrInvalidArgument):
		status = http.StatusBadRequest
	case errors.Is(err, biz.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, biz.ErrAlreadyExists), errors.Is(err, biz.ErrConflict):
		status = http.StatusConflict
	}
	writeRawJSON(w, status, map[string]any{
		"success":      false,
		"errorCode":    apiErrorCode(err),
		"errorMessage": err.Error(),
		"result":       nil,
	})
}

// apiErrorCode 将业务错误映射为 API 错误码。
func apiErrorCode(err error) string {
	switch {
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
