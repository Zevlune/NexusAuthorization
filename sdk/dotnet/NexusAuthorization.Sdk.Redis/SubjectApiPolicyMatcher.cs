namespace NexusAuthorization.Sdk.Redis;

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

        // ASP.NET Core 按不区分大小写匹配路由字面量。区分与不区分大小写两种匹配
        // 选中不同端点时直接拒绝，避免 /EXPORT 这类请求借宽模板访问未授权的具体路由。
        var (best, ambiguous) = SelectEndpoint(policy.Catalog ?? [], method, path, StringComparison.Ordinal);
        var (folded, foldedAmbiguous) = SelectEndpoint(policy.Catalog ?? [], method, path, StringComparison.OrdinalIgnoreCase);
        if (ambiguous || foldedAmbiguous || best?.Id != folded?.Id)
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
                string.IsNullOrWhiteSpace(endpoint.PathTemplate))
            {
                return false;
            }
        }

        return true;
    }

    private static (SubjectApiEndpoint? Best, bool Ambiguous) SelectEndpoint(
        IReadOnlyList<SubjectApiEndpoint?> catalog, string method, string path, StringComparison literalComparison)
    {
        SubjectApiEndpoint? best = null;
        var bestScore = -1;
        var ambiguous = false;

        foreach (var endpoint in catalog)
        {
            var candidate = endpoint!;
            if (!string.Equals(candidate.Method, method, StringComparison.OrdinalIgnoreCase) ||
                !TemplateMatches(candidate.PathTemplate, path, literalComparison))
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

        return (best, ambiguous);
    }

    private static bool TemplateMatches(string template, string path, StringComparison literalComparison)
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
            else if (!string.Equals(templatePart, pathParts[index], literalComparison))
            {
                return false;
            }
        }

        return true;
    }

    // 与匹配使用同一套规范化和占位符规则，尾部斜杠或 "{}" 不会改变优先级。
    private static int LiteralSegmentCount(string template)
    {
        var score = 0;
        foreach (var part in NormalizePath(template).Split('/'))
        {
            if (!IsParameter(part))
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
