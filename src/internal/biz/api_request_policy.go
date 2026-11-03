package biz

import "context"

// APIRequestPolicyRepository is the optional database fast path for one API
// authorization request. Implementations return a current policy snapshot
// scoped to the requested application, subject, domain and service. The
// snapshot must retain the complete service endpoint catalog so the normal
// policy compiler keeps route specificity and disabled-endpoint behavior.
type APIRequestPolicyRepository interface {
	GetAPIRequestPolicy(context.Context, string, string, string, string) (CurrentPolicy, error)
}
