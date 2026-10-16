package biz

import (
	"fmt"
	"strings"
)

// Frozen reference evaluator used only to verify indexed matching and benchmark it.
func evaluateCurrentPolicyLinear(state CurrentPolicy, input map[string]any) RBACDecision {
	d := RBACDecision{Effect: "deny", Reason: "denied"}
	snap := state.Data
	subject, _ := input["subject"].(string)
	domain, _ := input["domain"].(string)
	method, _ := input["method"].(string)
	path, _ := input["path"].(string)
	service, _ := input["service_resource"].(string)
	if service == "" {
		d.Reason = "service_resource_required"
		return d
	}
	endpoints, _ := snap["endpoints"].([]any)

	var endpointID string
	bestSpecificity := -1
	endpointEnabled := false
	ambiguous := false
	for _, raw := range endpoints {
		e, _ := raw.(map[string]any)
		template, _ := e["path_template"].(string)
		if e["service_resource"] != service || !strings.EqualFold(fmt.Sprint(e["method"]), method) || !matchesEndpointPath(template, path) {
			continue
		}
		specificity := 0
		for _, segment := range strings.Split(template, "/") {
			if !(strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")) {
				specificity++
			}
		}
		if specificity > bestSpecificity {
			bestSpecificity = specificity
			endpointID = fmt.Sprint(e["id"])
			endpointEnabled, _ = e["enabled"].(bool)
			ambiguous = false
		} else if specificity == bestSpecificity && endpointID != fmt.Sprint(e["id"]) {
			ambiguous = true
		}
	}
	if ambiguous {
		d.Reason = "ambiguous_endpoint"
		return d
	}
	if endpointID != "" && !endpointEnabled {
		d.Reason = "endpoint_disabled"
		return d
	}

	if endpointID == "" {
		d.Reason = "endpoint_not_found"
		return d
	}
	roles, _ := snap["roles"].([]any)
	bindings, _ := snap["subject_roles"].([]any)
	roleNames := []string{}
	ownedEndpoints := map[string]bool{}
	for _, raw := range bindings {
		b, _ := raw.(map[string]any)
		if fmt.Sprint(b["subject"]) != subject || fmt.Sprint(b["domain"]) != domain {
			continue
		}
		for _, rr := range roles {
			ro, _ := rr.(map[string]any)
			if fmt.Sprint(ro["id"]) == fmt.Sprint(b["role_id"]) {
				roleNames = append(roleNames, fmt.Sprint(ro["code"]))
				if enabled, _ := ro["enabled"].(bool); !enabled {
					continue
				}
				endpointIDs, _ := ro["endpoint_ids"].([]any)
				for _, rawEndpointID := range endpointIDs {
					ownedEndpoints[fmt.Sprint(rawEndpointID)] = true
				}
			}
		}
	}
	d.MatchedRoles = roleNames
	if !ownedEndpoints[endpointID] {
		d.Reason = "endpoint_not_granted"
		return d
	}
	d.Effect = "allow"
	d.Allowed = true
	d.Reason = "allowed"
	return d
}
