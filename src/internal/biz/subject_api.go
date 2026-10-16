package biz

import (
	"context"

	"github.com/google/uuid"
)

// SubjectAPIIdentity identifies one user's effective API permissions in one
// application, domain and service. ApplicationID is retained metadata from the
// aggregate; the authorization identity is the application key plus the
// normalized subject, domain and service.
type SubjectAPIIdentity struct {
	ApplicationKey  string    `json:"application_key"`
	ApplicationID   uuid.UUID `json:"application_id"`
	Subject         string    `json:"subject"`
	Domain          string    `json:"domain"`
	ServiceResource string    `json:"service_resource"`
	ServiceActive   bool      `json:"service_active"`
}

// SubjectAPIEndpoint contains only the route fields needed for authorization.
type SubjectAPIEndpoint struct {
	ID           uuid.UUID `json:"id"`
	Method       string    `json:"method"`
	PathTemplate string    `json:"path_template"`
	Enabled      bool      `json:"enabled"`
}

// SubjectAPIPolicy stores the deduplicated union of the user's enabled-role
// API grants. Catalog retains the requested service's routes, including
// ungranted/disabled routes, so specificity and ambiguity cannot widen grants.
// Menus and other users' bindings are never part of this cache.
type SubjectAPIPolicy struct {
	SubjectAPIIdentity
	APIs      []SubjectAPIEndpoint `json:"apis"`
	Catalog   []SubjectAPIEndpoint `json:"catalog"`
	RoleCodes []string             `json:"roles"`
}

// SubjectAPIRepository reads one user's aggregated service API policy. The
// implementation owns Redis and must return the authoritative aggregate for
// each call.
type SubjectAPIRepository interface {
	GetSubjectAPIs(context.Context, string, string, string, string) (SubjectAPIPolicy, error)
}
