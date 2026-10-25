namespace Demo.Api;

public static class SubjectApiPolicyMatcher
{
    public static AuthorizationDecision Evaluate(SubjectApiPolicy policy, string method, string path)
    {
        if (policy is null ||
            policy.Subject is null ||
            policy.ServiceResource is null)
        {
            return Deny("denied");
        }

        if (string.IsNullOrWhiteSpace(policy.Subject))
        {
            return Deny("denied");
        }

        var service = policy.ServiceResource.Trim();
        if (service.Length == 0)
            return Deny("service_resource_required");

        if (!policy.ServiceActive)
            return Deny("service_inactive");

        // 授权列表只使用 ID；方法、模板和启用状态以完整 Catalog 为准。
        if ((policy.APIs ?? []).Any(endpoint => endpoint is null || endpoint.Id == Guid.Empty) ||
            !AreValidEndpoints(policy.Catalog ?? []))
        {
            return Deny("denied");
        }

        if (method is null || path is null)
            return Deny("denied");

        SubjectApiEndpoint? best = null;
        var bestScore = -1;
        var ambiguous = false;

        foreach (var endpoint in policy.Catalog ?? [])
        {
            var candidate = endpoint!;
            if (!string.Equals(candidate.Method, method, StringComparison.OrdinalIgnoreCase) ||
                !TemplateMatches(candidate.PathTemplate, path))
            {
                continue;
            }

            var score = LiteralSegmentCount(candidate.PathTemplate);
            if (best is null || score > bestScore)
            {
                best = candidate;
                bestScore = score;
                ambiguous = false;
            }
            else if (score == bestScore && candidate.Id != best.Id)
            {
                ambiguous = true;
            }
        }

        if (ambiguous)
            return Deny("ambiguous_endpoint");
        if (best is null)
            return Deny("endpoint_not_found");
        if (!best.Enabled)
            return Deny("endpoint_disabled");

        foreach (var granted in policy.APIs ?? [])
        {
            if (granted!.Id == best.Id)
                return Allow();
        }

        return Deny("endpoint_not_granted");
    }

    private static bool AreValidEndpoints(IReadOnlyList<SubjectApiEndpoint?> endpoints)
    {
        foreach (var endpoint in endpoints)
        {
            if (endpoint is null ||
                endpoint.Id == Guid.Empty ||
                string.IsNullOrEmpty(endpoint.Method) ||
                endpoint.PathTemplate is null)
            {
                return false;
            }
        }

        return true;
    }

    private static bool TemplateMatches(string template, string path)
    {
        var templateParts = NormalizePath(template).Split('/');
        var pathParts = NormalizePath(path).Split('/');
        if (templateParts.Length != pathParts.Length)
            return false;

        for (var index = 0; index < templateParts.Length; index++)
        {
            var templatePart = templateParts[index];
            if (IsParameter(templatePart))
            {
                if (pathParts[index].Length == 0)
                    return false;
            }
            else if (!string.Equals(templatePart, pathParts[index], StringComparison.Ordinal))
            {
                return false;
            }
        }

        return true;
    }

    private static int LiteralSegmentCount(string template)
    {
        var score = 0;
        foreach (var part in template.Split('/'))
        {
            if (!(part.StartsWith('{') && part.EndsWith('}')))
                score++;
        }

        return score;
    }

    private static bool IsParameter(string segment) =>
        segment.Length > 2 && segment.StartsWith('{') && segment.EndsWith('}');

    private static string NormalizePath(string value) => value.Trim().Trim('/');

    private static AuthorizationDecision Allow() => new(true, "allow", "allowed");

    private static AuthorizationDecision Deny(string reason) => new(false, "deny", reason);
}
