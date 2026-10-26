using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Redis;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class SubjectApiPolicyMatcherTests
{
    [Fact]
    public void Concrete_path_takes_priority_even_when_only_parameter_path_is_granted()
    {
        var parameterized = Endpoint(Guid.NewGuid(), "GET", "/api/orders/{id}");
        var concrete = Endpoint(Guid.NewGuid(), "GET", "/api/orders/999");
        var policy = CreatePolicy(
            catalog: [parameterized, concrete],
            grants: [parameterized]);

        var decision = SubjectApiPolicyMatcher.Evaluate(
            policy,
            "GET",
            "/api/orders/999");

        AssertDenied("endpoint_not_granted", decision);
    }

    [Theory]
    [InlineData("/api/orders/42", "/api/orders/42")]
    [InlineData("/api/orders/{id}", "/api/orders/{orderId}")]
    public void Equal_priority_paths_with_different_ids_are_ambiguous(
        string firstTemplate,
        string secondTemplate)
    {
        var first = Endpoint(Guid.NewGuid(), "GET", firstTemplate);
        var second = Endpoint(Guid.NewGuid(), "GET", secondTemplate);
        var policy = CreatePolicy(catalog: [first, second], grants: [first, second]);

        var decision = SubjectApiPolicyMatcher.Evaluate(
            policy,
            "GET",
            "/api/orders/42");

        AssertDenied("ambiguous_endpoint", decision);
    }

    [Fact]
    public void Disabled_winning_concrete_path_does_not_fall_back_to_parameter()
    {
        var parameterized = Endpoint(Guid.NewGuid(), "GET", "/api/orders/{id}");
        var concrete = Endpoint(Guid.NewGuid(), "GET", "/api/orders/999", enabled: false);
        var policy = CreatePolicy(
            catalog: [parameterized, concrete],
            grants: [parameterized, concrete]);

        var decision = SubjectApiPolicyMatcher.Evaluate(
            policy,
            "GET",
            "/api/orders/999");

        AssertDenied("endpoint_disabled", decision);
    }

    [Theory]
    [InlineData("get")]
    [InlineData("GeT")]
    public void Request_method_is_matched_case_insensitively(string method)
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/api/orders/42");
        var policy = CreatePolicy(catalog: [endpoint], grants: [endpoint]);

        var decision = SubjectApiPolicyMatcher.Evaluate(
            policy,
            method,
            "/api/orders/42");

        AssertAllowed(decision);
    }

    [Theory]
    [InlineData("/API/orders/42")]
    [InlineData("/api/Orders/42")]
    public void Literal_path_segments_are_matched_case_sensitively(string path)
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/api/orders/42");
        var policy = CreatePolicy(catalog: [endpoint], grants: [endpoint]);

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, "GET", path);

        AssertDenied("endpoint_not_found", decision);
    }

    [Theory]
    [InlineData("/orders/42", "allowed")]
    [InlineData("/orders/42/items", "endpoint_not_found")]
    [InlineData("/orders/", "endpoint_not_found")]
    public void Parameter_consumes_exactly_one_nonempty_segment(
        string path,
        string expectedReason)
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/orders/{id}");
        var policy = CreatePolicy(catalog: [endpoint], grants: [endpoint]);

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, "GET", path);

        if (expectedReason == "allowed")
            AssertAllowed(decision);
        else
            AssertDenied(expectedReason, decision);
    }

    [Fact]
    public void Grant_uses_catalog_fields_and_matches_api_id_only()
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/orders/42");
        var grantedIdWithStaleFields = Endpoint(
            endpoint.Id,
            "POST",
            "/elsewhere",
            enabled: false);
        var policy = CreatePolicy(
            catalog: [endpoint],
            grants: [grantedIdWithStaleFields]);

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, "GET", "/orders/42");

        AssertAllowed(decision);
    }

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public void Missing_or_empty_catalog_never_falls_back_to_api_grants(bool nullCatalog)
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/orders/42");
        IReadOnlyList<SubjectApiEndpoint?>? catalog = nullCatalog ? null : [];
        var policy = CreatePolicy(catalog: catalog, grants: [endpoint]);

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, "GET", "/orders/42");

        AssertDenied("endpoint_not_found", decision);
    }

    [Fact]
    public void Catalog_match_without_api_grant_is_denied()
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/orders/42");
        var policy = CreatePolicy(catalog: [endpoint], grants: []);

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, "GET", "/orders/42");

        AssertDenied("endpoint_not_granted", decision);
    }

    [Theory]
    [InlineData("malformed_catalog")]
    [InlineData("missing_request_values")]
    public void Inactive_service_is_denied_before_catalog_and_request_validation(
        string defect)
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/orders/42");
        var catalog = defect == "malformed_catalog"
            ? new SubjectApiEndpoint?[] { null }
            : new SubjectApiEndpoint?[] { endpoint };
        var policy = CreatePolicy(
            catalog: catalog,
            grants: [endpoint],
            serviceActive: false);
        var method = defect == "missing_request_values" ? null! : "GET";
        var path = defect == "missing_request_values" ? null! : "/orders/42";

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, method, path);

        AssertDenied("service_inactive", decision);
    }

    [Theory]
    [InlineData(null, "orders.api", "denied")]
    [InlineData("", "orders.api", "denied")]
    [InlineData("user-1", null, "denied")]
    [InlineData("user-1", "", "service_resource_required")]
    public void Missing_or_empty_subject_and_service_are_denied(
        string? subject,
        string? service,
        string expectedReason)
    {
        var endpoint = Endpoint(Guid.NewGuid(), "GET", "/orders/42");
        var policy = CreatePolicy(
            subject: subject,
            service: service,
            catalog: [endpoint],
            grants: [endpoint]);

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, "GET", "/orders/42");

        AssertDenied(expectedReason, decision);
    }

    [Theory]
    [InlineData("null_entry")]
    [InlineData("empty_id")]
    [InlineData("empty_method")]
    [InlineData("empty_template")]
    [InlineData("whitespace_template")]
    public void Invalid_catalog_entries_are_denied(string defect)
    {
        var id = Guid.NewGuid();
        var endpoint = defect switch
        {
            "null_entry" => null,
            "empty_id" => Endpoint(Guid.Empty, "GET", "/", enabled: true),
            "empty_method" => Endpoint(id, "", "/", enabled: true),
            "empty_template" => Endpoint(id, "GET", "", enabled: true),
            "whitespace_template" => Endpoint(id, "GET", "   ", enabled: true),
            _ => throw new ArgumentOutOfRangeException(nameof(defect))
        };
        var policy = CreatePolicy(
            catalog: [endpoint],
            grants: [Endpoint(id, "GET", "/", enabled: true)]);

        var decision = SubjectApiPolicyMatcher.Evaluate(policy, "GET", "/");

        AssertDenied("denied", decision);
    }

    private static SubjectApiPolicy CreatePolicy(
        string? subject = "user-1",
        string? service = "orders.api",
        bool serviceActive = true,
        IReadOnlyList<SubjectApiEndpoint?>? catalog = null,
        IReadOnlyList<SubjectApiEndpoint?>? grants = null) =>
        new()
        {
            Subject = subject,
            ServiceResource = service,
            ServiceActive = serviceActive,
            Catalog = catalog,
            APIs = grants
        };

    private static SubjectApiEndpoint Endpoint(
        Guid id,
        string method,
        string pathTemplate,
        bool enabled = true) =>
        new(id, method, pathTemplate, enabled);

    private static void AssertAllowed(AuthorizationDecision decision) =>
        Assert.Equal(new AuthorizationDecision(true, "allow", "allowed"), decision);

    private static void AssertDenied(
        string reason,
        AuthorizationDecision decision) =>
        Assert.Equal(new AuthorizationDecision(false, "deny", reason), decision);
}
