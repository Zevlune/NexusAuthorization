package biz

import (
	"fmt"
	"strings"
	"unicode"
)

// A compiled policy is immutable after construction. There are no records
// scans, JSON conversions, or permission-map allocations during a decision.
type compiledRBACPolicy struct {
	routes     map[policyRouteScope]*policyRoutes
	principals map[policySubject]*policyPrincipal
	endpoints  []policyEndpoint
}

// policyRouteScope identifies one service/method route set. Keying by both
// fields keeps method names from colliding with service or path text.
type policyRouteScope struct{ service, method string }

// policyMethod canonicalizes HTTP methods for route lookup. HTTP methods are
// normally ASCII, but the full SimpleFold class is folded on the unusual
// Unicode path to preserve case-insensitive behavior.
func policyMethod(method string) string {
	for _, char := range method {
		if char > unicode.MaxASCII {
			return strings.Map(func(char rune) rune {
				minimum := char
				for folded := unicode.SimpleFold(char); folded != char; folded = unicode.SimpleFold(folded) {
					if folded < minimum {
						minimum = folded
					}
				}
				return minimum
			}, method)
		}
	}
	return strings.ToUpper(method)
}

// policySubject is the normalized identity key for a principal. Domain is
// normalized before lookup; an empty domain is the default scope.
type policySubject struct{ subject, domain string }

// policyPrincipal holds one subject's enabled-role bitmap and role codes.
// building is used only while compiling multiple bindings for that subject.
type policyPrincipal struct {
	roles roleBitmap
	names []string
	// Only multi-binding principals need a mutable builder during compilation.
	building map[uint32]uint64
}

// policyEndpoint is one compiled API catalog entry, including the enabled roles
// granted access to it.
type policyEndpoint struct {
	id      string
	enabled bool
	roles   roleBitmap
}

// Role ordinals are local to one compiled index. Only nonzero words are
// retained, so memory follows actual bindings/grants rather than users*APIs.
type roleWord struct {
	index uint32
	bits  uint64
}
type roleBitmap struct {
	words  []roleWord
	lookup map[uint32]uint64 // omitted for the common single-word bitmap
}

// freezeRoleBitmap converts the compiler's word map into the immutable sparse
// representation used for authorization. Only nonzero words are retained.
func freezeRoleBitmap(words map[uint32]uint64) roleBitmap {
	bitmap := roleBitmap{words: make([]roleWord, 0, len(words))}
	for index, bits := range words {
		bitmap.words = append(bitmap.words, roleWord{index, bits})
	}
	if len(words) > 1 {
		bitmap.lookup = words
	}
	return bitmap
}

// intersects reports whether the two role sets share an enabled role. Empty
// bitmaps never match; lookups account for word indices so bits in different
// 64-role words cannot authorize each other.
func (a roleBitmap) intersects(b roleBitmap) bool {
	if len(a.words) == 0 || len(b.words) == 0 {
		return false
	}
	if len(a.words) > len(b.words) {
		a, b = b, a
	}
	if len(b.words) == 1 {
		return a.words[0].index == b.words[0].index && a.words[0].bits&b.words[0].bits != 0
	}
	for _, word := range a.words {
		if word.bits&b.lookup[word.index] != 0 {
			return true
		}
	}
	return false
}

// addRole records an enabled role ordinal in the sparse bitmap being built.
func addRole(words map[uint32]uint64, ordinal uint32) {
	words[ordinal/64] |= uint64(1) << (ordinal % 64)
}

// policyRoutes groups routes for one service/method scope: exact paths in a
// hash map and parameter templates in tries indexed by path depth.
type policyRoutes struct {
	exact   map[string]routeMatch
	dynamic map[int]*policyRouteNode // parameter tries partitioned by path depth
}

// policyRouteNode is one segment node of a parameter-path trie. maxScore is the
// highest literal-segment count in the subtree and is used for pruning.
type policyRouteNode struct {
	literals map[string]*policyRouteNode
	param    *policyRouteNode
	match    routeMatch
	maxScore int
}

// routeMatch records the selected endpoint and its specificity. ambiguous is
// set when distinct endpoints tie at the best score, which must be denied
// instead of resolved by insertion order.
type routeMatch struct {
	endpoint  int
	score     int
	found     bool
	ambiguous bool
}

// mergeRouteMatch selects the higher-specificity candidate. Equal scores from
// different endpoint identities mark the result ambiguous so a runtime request
// cannot silently choose one route.
func mergeRouteMatch(best, candidate routeMatch, endpoints []policyEndpoint) routeMatch {
	if !candidate.found {
		return best
	}
	if !best.found || candidate.score > best.score {
		return candidate
	}
	if candidate.score == best.score && (candidate.ambiguous || endpoints[candidate.endpoint].id != endpoints[best.endpoint].id) {
		best.ambiguous = true
	}
	return best
}

// normalizedPolicyPath removes surrounding whitespace and slashes while
// preserving internal empty segments.
func normalizedPolicyPath(path string) string {
	return strings.Trim(strings.TrimSpace(path), "/")
}

// parameterSegment reports whether a path segment is an OpenAPI-style
// parameter placeholder such as "{id}".
func parameterSegment(segment string) bool {
	return len(segment) > 2 && strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

// add inserts a route template into the appropriate exact map or parameter trie.
// Candidates that tie for the best score are retained as ambiguous matches.
func (routes *policyRoutes) add(template string, match routeMatch, endpoints []policyEndpoint) {
	parts := strings.Split(normalizedPolicyPath(template), "/")
	hasParameter := false
	for _, part := range parts {
		hasParameter = hasParameter || parameterSegment(part)
	}
	if !hasParameter {
		key := normalizedPolicyPath(template)
		routes.exact[key] = mergeRouteMatch(routes.exact[key], match, endpoints)
		return
	}
	if routes.dynamic == nil {
		routes.dynamic = make(map[int]*policyRouteNode)
	}
	if routes.dynamic[len(parts)] == nil {
		routes.dynamic[len(parts)] = &policyRouteNode{}
	}
	node := routes.dynamic[len(parts)]
	for _, part := range parts {
		if match.score > node.maxScore {
			node.maxScore = match.score
		}
		if parameterSegment(part) {
			if node.param == nil {
				node.param = &policyRouteNode{}
			}
			node = node.param
		} else {
			if node.literals == nil {
				node.literals = make(map[string]*policyRouteNode)
			}
			if node.literals[part] == nil {
				node.literals[part] = &policyRouteNode{}
			}
			node = node.literals[part]
		}
	}
	if match.score > node.maxScore {
		node.maxScore = match.score
	}
	node.match = mergeRouteMatch(node.match, match, endpoints)
}

// search walks literal and parameter branches as needed, pruning subtrees that
// cannot beat the current match. Both branches may be explored because a
// literal choice can miss a more specific complete route or an equal-score
// conflict.
func (node *policyRouteNode) search(path string, terminal bool, best routeMatch, endpoints []policyEndpoint) routeMatch {
	if node == nil || (best.found && (node.maxScore < best.score || (best.ambiguous && node.maxScore == best.score))) {
		return best
	}
	if terminal {
		return mergeRouteMatch(best, node.match, endpoints)
	}
	segment, remaining, more := strings.Cut(path, "/")
	// Explore both branches when needed: choosing a literal greedily can miss
	// a more specific complete route, a fallback, or an equal-score conflict.
	best = node.literals[segment].search(remaining, !more, best, endpoints)
	if segment != "" {
		best = node.param.search(remaining, !more, best, endpoints)
	}
	return best
}

// find looks up the best endpoint for a path. Exact templates are considered
// first, then parameter tries at the same path depth when they could improve or
// tie the exact match.
func (routes *policyRoutes) find(path string, endpoints []policyEndpoint) routeMatch {
	key := normalizedPolicyPath(path)
	best := routes.exact[key]
	root := routes.dynamic[strings.Count(key, "/")+1]
	if root == nil || (best.found && root.maxScore < best.score) {
		return best
	}
	return root.search(key, false, best, endpoints)
}

// policyString converts a decoded JSON value to the string form used by policy
// keys and IDs. Callers only expect strings here; non-strings are diagnostic
// fallbacks rather than authorization input.
func policyString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

// compileRBACPolicy builds the immutable route, endpoint-role, and
// principal-role indexes used by authorization decisions. Compilation does not
// mutate the source policy and never expands every subject into every endpoint.
func compileRBACPolicy(policy CurrentPolicy) *compiledRBACPolicy {
	p := &compiledRBACPolicy{routes: make(map[policyRouteScope]*policyRoutes), principals: make(map[policySubject]*policyPrincipal)}
	rawEndpoints, _ := policy.Data["endpoints"].([]any)
	endpointOrdinals := make(map[string][]int, len(rawEndpoints))
	for _, raw := range rawEndpoints {
		e, _ := raw.(map[string]any)
		service, _ := e["service_resource"].(string)
		template, _ := e["path_template"].(string)
		enabled, _ := e["enabled"].(bool)
		id := policyString(e["id"])
		ordinal := len(p.endpoints)
		p.endpoints = append(p.endpoints, policyEndpoint{id: id, enabled: enabled})
		endpointOrdinals[id] = append(endpointOrdinals[id], ordinal)
		scope := policyRouteScope{service, policyMethod(policyString(e["method"]))}
		routes := p.routes[scope]
		if routes == nil {
			routes = &policyRoutes{exact: make(map[string]routeMatch)}
			p.routes[scope] = routes
		}
		score := 0
		for _, part := range strings.Split(template, "/") {
			if !(strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")) {
				score++
			}
		}
		routes.add(template, routeMatch{endpoint: ordinal, score: score, found: true}, p.endpoints)
	}

	type indexedRole struct {
		ordinal   uint32
		code      string
		enabled   bool
		principal *policyPrincipal
	}
	rawRoles, _ := policy.Data["roles"].([]any)
	roles := make(map[string][]indexedRole, len(rawRoles))
	endpointWords := make([]map[uint32]uint64, len(p.endpoints))
	for ordinal, raw := range rawRoles {
		r, _ := raw.(map[string]any)
		enabled, _ := r["enabled"].(bool)
		role := indexedRole{ordinal: uint32(ordinal), code: policyString(r["code"]), enabled: enabled}
		role.principal = &policyPrincipal{names: []string{role.code}}
		if enabled {
			role.principal.roles = roleBitmap{words: []roleWord{{uint32(ordinal) / 64, uint64(1) << (uint32(ordinal) % 64)}}}
		}
		id := policyString(r["id"])
		roles[id] = append(roles[id], role)
		if !enabled {
			continue
		}
		grants, _ := r["endpoint_ids"].([]any)
		for _, grant := range grants {
			for _, index := range endpointOrdinals[policyString(grant)] {
				if endpointWords[index] == nil {
					endpointWords[index] = make(map[uint32]uint64)
				}
				addRole(endpointWords[index], role.ordinal)
			}
		}
	}
	for index, words := range endpointWords {
		p.endpoints[index].roles = freezeRoleBitmap(words)
	}

	bindings, _ := policy.Data["subject_roles"].([]any)
	for _, raw := range bindings {
		binding, _ := raw.(map[string]any)
		key := policySubject{policyString(binding["subject"]), NormalizeRBACDomain(policyString(binding["domain"]))}
		for _, role := range roles[policyString(binding["role_id"])] {
			principal := p.principals[key]
			if principal == nil {
				// All users with one binding to this role share the same immutable
				// principal. Never materialize the role's API list per user.
				p.principals[key] = role.principal
				continue
			}
			if principal.building == nil {
				// Copy on the second binding; shared singleton stays immutable.
				words := make(map[uint32]uint64)
				for _, word := range principal.roles.words {
					words[word.index] = word.bits
				}
				principal = &policyPrincipal{names: append([]string(nil), principal.names...), building: words}
				p.principals[key] = principal
			}
			// Preserve diagnostic order, including disabled roles and duplicates.
			principal.names = append(principal.names, role.code)
			if role.enabled {
				addRole(principal.building, role.ordinal)
			}
		}
	}
	for _, principal := range p.principals {
		if principal.building != nil {
			principal.roles = freezeRoleBitmap(principal.building)
			principal.building = nil
		}
	}
	return p
}

// Authorize evaluates a request against the compiled index. Missing or disabled
// endpoints and missing role grants deny; there is no default-allow path.
func (p *compiledRBACPolicy) Authorize(input map[string]any) RBACDecision {
	d := RBACDecision{Effect: "deny", Reason: "denied"}
	service, _ := input["service_resource"].(string)
	if service == "" {
		d.Reason = "service_resource_required"
		return d
	}
	method, _ := input["method"].(string)
	path, _ := input["path"].(string)
	routes := p.routes[policyRouteScope{service, policyMethod(method)}]
	if routes == nil {
		d.Reason = "endpoint_not_found"
		return d
	}
	match := routes.find(path, p.endpoints)
	if match.ambiguous {
		d.Reason = "ambiguous_endpoint"
		return d
	}
	if !match.found {
		d.Reason = "endpoint_not_found"
		return d
	}
	endpoint := &p.endpoints[match.endpoint]
	if !endpoint.enabled {
		d.Reason = "endpoint_disabled"
		return d
	}
	subject, _ := input["subject"].(string)
	domain, _ := input["domain"].(string)
	d.MatchedRoles = []string{}
	principal := p.principals[policySubject{subject, NormalizeRBACDomain(domain)}]
	if principal != nil {
		// Do not expose the immutable index's backing array to callers.
		d.MatchedRoles = append([]string(nil), principal.names...)
	}
	if principal == nil || !principal.roles.intersects(endpoint.roles) {
		d.Reason = "endpoint_not_granted"
		return d
	}
	d.Effect, d.Allowed, d.Reason = "allow", true, "allowed"
	return d
}
