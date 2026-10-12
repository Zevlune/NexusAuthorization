package httpserver

import (
	"net/http"
	"strings"

	"github.com/luck/permission-center-go/internal/biz"
)

func (h *Handler) ListApplications(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	items, err := h.rbacV1.ListApplications(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) SaveApplication(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	var body struct {
		Key         string  `json:"key"`
		DisplayName string  `json:"display_name"`
		Description string  `json:"description"`
		URL         *string `json:"url"`
		Enabled     *bool   `json:"enabled"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	value := biz.Application{Key: strings.TrimSpace(body.Key), DisplayName: body.DisplayName, Description: body.Description, Enabled: enabled}
	if body.URL != nil {
		value.URL = *body.URL
	}
	status := http.StatusCreated
	if r.Method == http.MethodPut {
		value.Key = strings.TrimSpace(r.PathValue("key"))
		if value.Key == "" {
			writeError(w, biz.ErrInvalidArgument)
			return
		}
		applications, err := h.rbacV1.ListApplications(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		for _, application := range applications {
			if application.Key == value.Key {
				value.ID = application.ID
				if body.URL == nil {
					value.URL = application.URL
				}
				if body.Enabled == nil {
					value.Enabled = application.Enabled
				}
				break
			}
		}
		if value.ID == (biz.Application{}).ID {
			writeError(w, biz.ErrNotFound)
			return
		}
		status = http.StatusOK
	}
	saved, err := h.rbacV1.SaveApplication(r.Context(), value)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, status, saved)
}

func (h *Handler) ListApplicationServices(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	key := strings.TrimSpace(r.PathValue("key"))
	items, err := h.rbacV1.ListApplicationServices(r.Context(), key)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) ReplaceApplicationServices(w http.ResponseWriter, r *http.Request) {
	if h.rbacV1 == nil {
		writeError(w, biz.ErrNotFound)
		return
	}
	var body struct {
		ServiceResourceKeys []string `json:"service_resource_keys"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, err)
		return
	}
	if err := h.rbacV1.ReplaceApplicationServices(r.Context(), r.PathValue("key"), body.ServiceResourceKeys); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, nil)
}
