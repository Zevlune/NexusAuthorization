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

// SubjectAPIPublication is the Redis publication state of one application.
// Requested counts committed saves that asked for a rebuild; Published is the
// highest request number whose changes the current records include.
type SubjectAPIPublication struct {
	State     string
	Requested int64
	Published int64
}

// Ready reports whether the records are readable and include every save that
// requested a rebuild.
func (p SubjectAPIPublication) Ready() bool {
	return p.State == "ready" && p.Published >= p.Requested
}

// SubjectAPILease is an exclusive, fenced right to rebuild the Redis records of
// a set of applications. Every write is rejected once the lease is lost, so a
// stalled holder can never overwrite a newer rebuild.
type SubjectAPILease interface {
	// Pending returns the leased applications whose records do not yet cover
	// the given request numbers, or whose gate is not ready.
	Pending(context.Context, map[string]int64) ([]string, error)
	// Block closes the read gate of the given applications.
	Block(context.Context, []string) error
	// Replace rewrites one application's records and reopens its gate.
	Replace(context.Context, string, []SubjectAPIPolicy) error
	// Release gives the lease up; it is safe to call more than once.
	Release(context.Context)
}
