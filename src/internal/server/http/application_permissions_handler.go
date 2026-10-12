package httpserver

import (
	"github.com/google/uuid"
	"github.com/luck/permission-center-go/internal/biz"
	"net/http"
	"strings"
)

func (h *Handler) ListApplicationMenus(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if app == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	items, err := h.rbacV1.ListApplicationMenus(r.Context(), app)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h *Handler) SaveApplicationMenu(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if app == "" {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	var body biz.ApplicationMenu
	if decodeJSON(r, &body) != nil {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	v, err := h.rbacV1.SaveApplicationMenu(r.Context(), app, body)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
func (h *Handler) DeleteApplicationMenu(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app_key"))
	id, err := uuid.Parse(r.PathValue("id"))
	if app == "" || err != nil {
		writeError(w, biz.ErrInvalidArgument)
		return
	}
	if err = h.rbacV1.DeleteApplicationMenu(r.Context(), app, id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"success": true})
}
func (h *Handler) SubjectApplicationMenus(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	q := r.URL.Query()
	items, err := h.rbacV1.SubjectMenus(r.Context(), q.Get("app_key"), q.Get("subject"), q.Get("domain"))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
