package httpserver

import (
	"github.com/google/uuid"
	"github.com/luck/nexus-authorization/internal/biz"
	"net/http"
	"strings"
)

func (h *Handler) ListApplicationMenus(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if app == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	items, err := h.rbacV1.ListApplicationMenus(r.Context(), app)
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (h *Handler) SaveApplicationMenu(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app_key"))
	if app == "" {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	var body biz.ApplicationMenu
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, err)
		return
	}
	v, err := h.rbacV1.SaveApplicationMenu(r.Context(), app, body)
	if err != nil {
		writeError(w, r, err)
		return
	}
	status := http.StatusOK
	if body.ID == uuid.Nil {
		status = http.StatusCreated
	}
	writeJSON(w, status, v)
}
func (h *Handler) DeleteApplicationMenu(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	app := strings.TrimSpace(r.URL.Query().Get("app_key"))
	id, err := uuid.Parse(r.PathValue("id"))
	if app == "" || err != nil {
		writeError(w, r, biz.ErrInvalidArgument)
		return
	}
	if err = h.rbacV1.DeleteApplicationMenu(r.Context(), app, id); err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
func (h *Handler) SubjectApplicationMenus(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, r, biz.ErrNotFound)
		return
	}
	q := r.URL.Query()
	items, err := h.rbacV1.SubjectMenus(r.Context(), q.Get("app_key"), q.Get("subject"), q.Get("domain"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
