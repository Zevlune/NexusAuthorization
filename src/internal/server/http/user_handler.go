package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/luck/nexus-authorization/internal/biz"
)

// SCIMUserDirectory 只读取身份平台的用户，不维护本地用户记录。
type SCIMUserDirectory interface {
	ListUsers(context.Context, string, int, int) (*biz.SCIMUserList, error)
}

func (h *Handler) WithUserDirectory(directory SCIMUserDirectory) *Handler {
	// Avoid storing an unconfigured typed nil client in an interface.
	if client, ok := directory.(*biz.NexusAuthSCIMClient); ok && client == nil {
		h.users = nil
	} else {
		h.users = directory
	}
	return h
}

// ListUsers 通过 NexusAuth SCIM API 查询用户列表，保留 SCIM 分页结构。
func (h *Handler) ListUsers(w http.ResponseWriter, r *http.Request) {
	startIndex, err := userListInt(r, "startIndex", 1, 1, 0)
	if err != nil {
		writeError(w, r, err)
		return
	}
	count, err := userListInt(r, "count", 100, 0, 200)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if h.users == nil {
		writeUserDirectoryError(w, http.StatusServiceUnavailable, "SCIM_NOT_CONFIGURED", "NexusAuth SCIM user directory is not configured")
		return
	}
	users, err := h.users.ListUsers(r.Context(), r.URL.Query().Get("filter"), startIndex, count)
	if err != nil {
		// Never expose an upstream response or transport error to the browser.
		writeUserDirectoryError(w, http.StatusBadGateway, "SCIM_UPSTREAM_ERROR", "Unable to retrieve the NexusAuth SCIM user list")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, users)
}

func userListInt(r *http.Request, name string, fallback, minimum, maximum int) (int, error) {
	values, present := r.URL.Query()[name]
	if !present {
		return fallback, nil
	}
	if len(values) != 1 {
		return 0, fmt.Errorf("%w: %s must have one integer value", biz.ErrInvalidArgument, name)
	}
	value, err := strconv.Atoi(values[0])
	if err != nil || value < minimum || (maximum > 0 && value > maximum) {
		return 0, fmt.Errorf("%w: invalid %s", biz.ErrInvalidArgument, name)
	}
	return value, nil
}

func writeUserDirectoryError(w http.ResponseWriter, status int, code, message string) {
	writeRawJSON(w, status, map[string]any{"success": false, "errorCode": code, "errorMessage": message, "result": nil})
}
