using System.Net;
using System.Net.Http.Json;
using System.Text.Json.Nodes;
using Demo.Api;
using Microsoft.Extensions.DependencyInjection;
using StackExchange.Redis;
using Xunit;

namespace Demo.Api.IntegrationTests;

public sealed class RedisAuthorizationTests(RedisFixture fixture) : IClassFixture<RedisFixture>
{
    private readonly RedisFixture _fixture = fixture;

    [Theory]
    [MemberData(nameof(DemoEndpoints.Cases), MemberType = typeof(DemoEndpoints))]
    public async Task Granted_policy_allows_each_real_controller_action(
        string method, string path, string template, int successStatus)
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        Assert.Contains(policy["catalog"]!.AsArray().OfType<JsonObject>(), endpoint =>
            Text(endpoint, "method") == method && Text(endpoint, "path_template") == template);

        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();
        using var response = await SendAsync(client, method, path);

        Assert.Equal(successStatus, (int)response.StatusCode);
        await AssertSuccessfulBodyAsync(response, method, path);
    }

    [Theory]
    [MemberData(nameof(AnonymousCases))]
    public async Task Anonymous_requests_to_all_protected_actions_return_401(
        string method, string path)
    {
        using var factory = _fixture.CreateFactory();
        using var client = factory.CreateClient();
        using var response = await SendAsync(client, method, path);

        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

    [Fact]
    public async Task Wrong_or_missing_subject_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        using var wrongSubjectClient = factory.CreateAuthenticatedClient(Guid.NewGuid().ToString());
        using var missingSubjectClient = factory.CreateAuthenticatedClient(subject: null);
        using var wrongSubject = await wrongSubjectClient.GetAsync("/api/demo/products");
        using var missingSubject = await missingSubjectClient.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, wrongSubject.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, missingSubject.StatusCode);
    }

    [Fact]
    public async Task Get_grant_does_not_authorize_post_on_the_same_path()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        var getProduct = FindEndpoint(policy, "GET", "/api/demo/products");
        SetApiGrants(policy, getProduct);
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var granted = await client.GetAsync("/api/demo/products");
        using var denied = await client.PostAsJsonAsync("/api/demo/products",
            new { name = "不应被创建", price = 9.99m });

        Assert.Equal(HttpStatusCode.OK, granted.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, denied.StatusCode);
    }

    [Fact]
    public async Task Revocation_takes_effect_immediately_and_regrant_is_allowed()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        using var client = factory.CreateAuthenticatedClient();

        using var initiallyAllowed = await client.GetAsync("/api/demo/products");
        Assert.Equal(HttpStatusCode.OK, initiallyAllowed.StatusCode);

        var revoked = TestPolicies.Create(factory);
        revoked["apis"] = new JsonArray();
        await _fixture.PublishAsync(factory, revoked);
        using var afterRevocation = await client.GetAsync("/api/demo/products");
        Assert.Equal(HttpStatusCode.Forbidden, afterRevocation.StatusCode);

        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        using var afterRegrant = await client.GetAsync("/api/demo/products");
        Assert.Equal(HttpStatusCode.OK, afterRegrant.StatusCode);
    }

    [Fact]
    public async Task Updating_state_is_denied_even_when_policy_exists()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory), state: "updating");
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Missing_state_is_denied_even_when_policy_exists()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        await _fixture.Database.KeyDeleteAsync(_fixture.StateKey(factory));
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Missing_policy_record_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        await _fixture.Database.HashDeleteAsync(
            _fixture.PolicyKey(factory), _fixture.PolicyField(factory));
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Malformed_policy_json_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        await _fixture.Database.HashSetAsync(
            _fixture.PolicyKey(factory), _fixture.PolicyField(factory), "{not-json");
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Policy_stored_under_wrong_subject_key_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        var otherSubject = Guid.NewGuid().ToString();
        var policy = TestPolicies.Create(factory);
        policy["subject"] = otherSubject;
        await _fixture.PublishAsync(factory, policy, subject: TestIdentity.Subject);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Static_catalog_entry_takes_priority_and_its_missing_grant_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        var parameterized = FindEndpoint(policy, "GET", "/api/demo/orders/{id}");
        var staticEndpoint = Endpoint(Guid.NewGuid(), "GET", "/api/demo/orders/999");
        Catalog(policy).Add(staticEndpoint.DeepClone());
        SetApiGrants(policy, parameterized);
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var parameterizedAllowed = await client.GetAsync("/api/demo/orders/1");
        using var staticDenied = await client.GetAsync("/api/demo/orders/999");

        Assert.Equal(HttpStatusCode.OK, parameterizedAllowed.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, staticDenied.StatusCode);
    }

    [Fact]
    public async Task Disabled_static_catalog_entry_overrides_an_authorized_parameter()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        var parameterized = FindEndpoint(policy, "GET", "/api/demo/orders/{id}");
        var disabled = Endpoint(Guid.NewGuid(), "GET", "/api/demo/orders/999", enabled: false);
        Catalog(policy).Add(disabled.DeepClone());
        SetApiGrants(policy, parameterized, disabled);
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var parameterizedAllowed = await client.GetAsync("/api/demo/orders/1");
        using var disabledDenied = await client.GetAsync("/api/demo/orders/999");

        Assert.Equal(HttpStatusCode.OK, parameterizedAllowed.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, disabledDenied.StatusCode);
    }

    [Fact]
    public async Task Equal_scoring_catalog_matches_with_different_ids_are_ambiguous()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        var original = FindEndpoint(policy, "GET", "/api/demo/orders/{id}");
        var duplicate = Endpoint(Guid.NewGuid(), "GET", "/api/demo/orders/{id}");
        Catalog(policy).Add(duplicate.DeepClone());
        SetApiGrants(policy, original, duplicate);
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/orders/1");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Null_catalog_does_not_fall_back_to_authorized_api_ids()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        policy["catalog"] = null;
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Api_grants_are_matched_by_id_only()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        var authorizedIdOnly = FindEndpoint(policy, "GET", "/api/demo/products").DeepClone().AsObject();
        authorizedIdOnly["method"] = "POST";
        authorizedIdOnly["path_template"] = "/not-the-requested-route";
        authorizedIdOnly["enabled"] = false;
        SetApiGrants(policy, authorizedIdOnly);
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Fact]
    public async Task Domain_scoped_request_does_not_use_global_policy()
    {
        using var factory = _fixture.CreateFactory(domain: "tenant-a");
        var scopedPolicy = TestPolicies.Create(factory);
        scopedPolicy["domain"] = "tenant-a";
        await _fixture.PublishAsync(factory, scopedPolicy);
        using var client = factory.CreateAuthenticatedClient();

        using var scopedAllowed = await client.GetAsync("/api/demo/products");
        Assert.Equal(HttpStatusCode.OK, scopedAllowed.StatusCode);

        var globalPolicy = TestPolicies.Create(factory);
        globalPolicy["domain"] = "";
        var globalKey = GlobalPolicyKey(factory);
        await _fixture.Database.HashSetAsync(
            globalKey, _fixture.PolicyField(factory), globalPolicy.ToJsonString());
        await _fixture.Database.HashDeleteAsync(
            _fixture.PolicyKey(factory), _fixture.PolicyField(factory));
        using var withoutScopedRecord = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, withoutScopedRecord.StatusCode);
    }

    [Fact]
    public async Task Global_domain_is_normalized_to_the_empty_domain_key()
    {
        using var factory = _fixture.CreateFactory(domain: "global");
        var policy = TestPolicies.Create(factory);
        policy["domain"] = "global";
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(GlobalPolicyKey(factory), _fixture.PolicyKey(factory).ToString());
        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Fact]
    public void Connection_multiplexer_is_singleton_across_di_scopes()
    {
        using var factory = _fixture.CreateFactory();
        using var firstScope = factory.Services.CreateScope();
        using var secondScope = factory.Services.CreateScope();

        var first = firstScope.ServiceProvider.GetRequiredService<IConnectionMultiplexer>();
        var second = secondScope.ServiceProvider.GetRequiredService<IConnectionMultiplexer>();

        Assert.Same(first, second);
    }

    [Fact]
    public async Task Go_compatible_escaped_keys_are_used_by_the_real_http_pipeline()
    {
        const string escapedAppKey = "demo+app%2B%E6%B5%8B%E8%AF%95%3F%3A";
        const string escapedService = "demo+api%2F%E6%B5%8B%E8%AF%95%3Fx%3D1%26y%3D2";
        const string escapedDomain = "%E7%A7%9F%E6%88%B7+A%2F%E4%B8%80%E5%8F%B7";

        using var factory = _fixture.CreateFactory(
            appKey: "demo app+测试?:",
            serviceResource: "demo api/测试?x=1&y=2",
            domain: "租户 A/一号");
        var prefix = factory.Settings.Redis.KeyPrefix;
        var expectedStateKey = $"{prefix}:rbac:api:app:{escapedAppKey}:state";
        var expectedPolicyKey =
            $"{prefix}:{escapedService}:{TestIdentity.Subject}:{escapedDomain}";

        Assert.Equal(expectedStateKey, _fixture.StateKey(factory).ToString());
        Assert.Equal(expectedPolicyKey, _fixture.PolicyKey(factory).ToString());
        Assert.DoesNotContain(escapedAppKey, expectedPolicyKey, StringComparison.Ordinal);

        var policy = TestPolicies.Create(factory);
        await _fixture.Database.HashSetAsync(
            new RedisKey(expectedPolicyKey), _fixture.PolicyField(factory), policy.ToJsonString());
        await _fixture.Database.StringSetAsync(new RedisKey(expectedStateKey), "ready");
        using var client = factory.CreateAuthenticatedClient();
        using var response = await client.GetAsync("/api/demo/orders");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        await AssertSuccessfulBodyAsync(response, "GET", "/api/demo/orders");
    }

    [Fact]
    public async Task Shared_service_hash_keeps_application_revocations_isolated()
    {
        var prefix = $"nexusauthorization:integration:{Guid.NewGuid():N}";
        using var firstFactory = _fixture.CreateFactory(appKey: "demo.first", keyPrefix: prefix);
        using var secondFactory = _fixture.CreateFactory(appKey: "demo.second", keyPrefix: prefix);
        var policyKey = _fixture.PolicyKey(firstFactory);

        Assert.Equal(policyKey, _fixture.PolicyKey(secondFactory));
        Assert.NotEqual(_fixture.StateKey(firstFactory), _fixture.StateKey(secondFactory));
        Assert.NotEqual(_fixture.PolicyField(firstFactory), _fixture.PolicyField(secondFactory));

        await _fixture.PublishAsync(firstFactory, TestPolicies.Create(firstFactory));
        await _fixture.PublishAsync(secondFactory, TestPolicies.Create(secondFactory));
        using var firstClient = firstFactory.CreateAuthenticatedClient();
        using var secondClient = secondFactory.CreateAuthenticatedClient();
        using var firstAllowed = await firstClient.GetAsync("/api/demo/products");
        using var secondAllowed = await secondClient.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.OK, firstAllowed.StatusCode);
        Assert.Equal(HttpStatusCode.OK, secondAllowed.StatusCode);

        await _fixture.Database.HashDeleteAsync(
            policyKey, _fixture.PolicyField(secondFactory));
        using var firstAfterSecondRevocation = await firstClient.GetAsync("/api/demo/products");
        using var secondAfterRevocation = await secondClient.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.OK, firstAfterSecondRevocation.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, secondAfterRevocation.StatusCode);
    }

    [Fact]
    public async Task Authorized_business_failures_keep_their_400_and_404_contracts()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        using var client = factory.CreateAuthenticatedClient();

        using var invalid = await client.PostAsJsonAsync("/api/demo/products",
            new { name = "", price = 0m });
        using var missing = await client.GetAsync("/api/demo/products/999");

        Assert.Equal(HttpStatusCode.BadRequest, invalid.StatusCode);
        Assert.Equal(HttpStatusCode.NotFound, missing.StatusCode);
    }

    [Fact]
    public async Task Auth_me_requires_authentication_but_not_an_authorization_record()
    {
        using var factory = _fixture.CreateFactory();
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/auth/me");
        var body = JsonNode.Parse(await response.Content.ReadAsStringAsync())!.AsObject();

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal(TestIdentity.Subject, body["subject"]!.GetValue<string>());
    }

    private static async Task<HttpResponseMessage> SendAsync(HttpClient client, string method, string path)
    {
        using var request = new HttpRequestMessage(new HttpMethod(method), path);
        if (method == "POST" && path == "/api/demo/products")
            request.Content = JsonContent.Create(new { name = "集成测试商品", price = 49.90m });
        else if (method == "PUT")
            request.Content = JsonContent.Create(new { name = "集成测试商品", price = 59.90m });

        return await client.SendAsync(request);
    }

    private static async Task AssertSuccessfulBodyAsync(
        HttpResponseMessage response, string method, string path)
    {
        if (path == "/api/demo/reports/export")
        {
            Assert.Equal("text/csv", response.Content.Headers.ContentType?.MediaType);
            var csv = await response.Content.ReadAsStringAsync();
            Assert.Contains("id,number,amount", csv, StringComparison.Ordinal);
            Assert.Contains("DEMO-001", csv, StringComparison.Ordinal);
            return;
        }

        var body = JsonNode.Parse(await response.Content.ReadAsStringAsync());
        switch ((method, path))
        {
            case ("GET", "/api/demo/orders"):
                Assert.Equal("DEMO-001", body!.AsArray()[0]!["number"]!.GetValue<string>());
                break;
            case ("GET", "/api/demo/orders/1"):
                Assert.Equal(1, body!["id"]!.GetValue<int>());
                Assert.Equal("DEMO-001", body["number"]!.GetValue<string>());
                break;
            case ("GET", "/api/demo/products"):
                Assert.Equal(1, body!.AsArray()[0]!["id"]!.GetValue<int>());
                Assert.Equal("演示商品一", body.AsArray()[0]!["name"]!.GetValue<string>());
                break;
            case ("GET", "/api/demo/products/1"):
                Assert.Equal(1, body!["id"]!.GetValue<int>());
                Assert.Equal(99.00m, body["price"]!.GetValue<decimal>());
                break;
            case ("POST", "/api/demo/products"):
                Assert.Equal(3, body!["id"]!.GetValue<int>());
                Assert.False(body["persisted"]!.GetValue<bool>());
                Assert.EndsWith("/api/demo/products/3",
                    response.Headers.Location?.OriginalString, StringComparison.Ordinal);
                break;
            case ("PUT", "/api/demo/products/1"):
                Assert.Equal(1, body!["id"]!.GetValue<int>());
                Assert.False(body["persisted"]!.GetValue<bool>());
                break;
            case ("DELETE", "/api/demo/products/1"):
                Assert.Equal(1, body!["id"]!.GetValue<int>());
                Assert.False(body["persisted"]!.GetValue<bool>());
                break;
            case ("GET", "/api/demo/reports/summary"):
                Assert.Equal(2, body!["orderCount"]!.GetValue<int>());
                Assert.Equal(298.00m, body["totalAmount"]!.GetValue<decimal>());
                break;
            default:
                throw new Xunit.Sdk.XunitException($"未定义成功响应断言：{method} {path}");
        }
    }

    private static JsonArray Catalog(JsonObject policy) =>
        policy["catalog"] as JsonArray
        ?? throw new InvalidOperationException("测试策略缺少 catalog。");

    public static IEnumerable<object[]> AnonymousCases =>
        DemoEndpoints.All.Select(endpoint => new object[]
        {
            endpoint.Method,
            endpoint.Path
        });

    private static JsonObject FindEndpoint(JsonObject policy, string method, string template) =>
        Catalog(policy).OfType<JsonObject>().Single(endpoint =>
            Text(endpoint, "method") == method && Text(endpoint, "path_template") == template);

    private static string Text(JsonObject value, string property) =>
        value[property]?.GetValue<string>()
        ?? throw new InvalidOperationException($"测试策略缺少 {property}。");

    private static JsonObject Endpoint(
        Guid id, string method, string template, bool enabled = true) =>
        new()
        {
            ["id"] = id.ToString(),
            ["method"] = method,
            ["path_template"] = template,
            ["enabled"] = enabled
        };

    private static void SetApiGrants(JsonObject policy, params JsonObject[] endpoints)
    {
        var apis = new JsonArray();
        foreach (var endpoint in endpoints)
            apis.Add(endpoint.DeepClone());
        policy["apis"] = apis;
    }

    private static string GlobalPolicyKey(AuthorizationApiFactory factory)
    {
        var settings = factory.Settings;
        var center = settings.AuthorizationCenter;
        return $"{settings.Redis.KeyPrefix}:{RedisAuthorizationStore.QueryEscape(center.ServiceResource.Trim())}:" +
               RedisAuthorizationStore.QueryEscape(TestIdentity.Subject);
    }
}
