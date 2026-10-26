using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Redis;
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
    public async Task Revocation_by_policy_update_or_key_deletion_takes_effect_immediately()
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

        await _fixture.Database.KeyDeleteAsync(_fixture.PolicyKey(factory));
        using var afterDeletion = await client.GetAsync("/api/demo/products");
        Assert.Equal(HttpStatusCode.Forbidden, afterDeletion.StatusCode);
    }

    [Fact]
    public async Task Missing_string_policy_is_denied_as_endpoint_not_granted()
    {
        using var factory = _fixture.CreateFactory();
        using var scope = factory.Services.CreateScope();
        var store = scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();

        var decision = await store.AuthorizeAsync(
            TestIdentity.Subject, "GET", "/api/demo/products", CancellationToken.None);

        Assert.False(decision.Allowed);
        Assert.Equal("endpoint_not_granted", decision.Reason);
    }

    [Fact]
    public async Task Malformed_policy_json_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.PublishAsync(factory, TestPolicies.Create(factory));
        await _fixture.Database.StringSetAsync(_fixture.PolicyKey(factory), "{not-json");
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Legacy_hash_policy_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        await _fixture.Database.HashSetAsync(
            _fixture.PolicyKey(factory), factory.Settings.AuthorizationCenter.AppKey,
            TestPolicies.Create(factory).ToJsonString());
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Theory]
    [InlineData("legacy.application")]
    [InlineData(" ")]
    public async Task Non_empty_application_identity_in_string_policy_is_denied(
        string applicationKey)
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        policy["application_key"] = applicationKey;
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Empty_application_identity_remains_compatible_and_is_ignored()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        policy["application_key"] = "";
        policy["application_id"] = Guid.Empty.ToString();
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
    }

    [Fact]
    public async Task Non_zero_application_id_in_string_policy_is_denied()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        policy["application_id"] = Guid.NewGuid().ToString();
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Theory]
    [InlineData("subject")]
    [InlineData("service_resource")]
    [InlineData("domain")]
    public async Task Policy_identity_must_exactly_match_the_request_scope(string field)
    {
        using var factory = _fixture.CreateFactory(domain: "tenant-a");
        var policy = TestPolicies.Create(factory);
        policy[field] = field == "domain" ? "tenant-b" : $"other-{field}";
        await _fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Invalid_json_field_types_are_denied()
    {
        using var factory = _fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        policy["service_active"] = "true";
        await _fixture.PublishAsync(factory, policy);
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
        await _fixture.Database.StringSetAsync(globalKey, globalPolicy.ToJsonString());
        await _fixture.Database.KeyDeleteAsync(_fixture.PolicyKey(factory));
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
    public async Task Scoped_providers_share_a_connection_that_survives_scope_disposal()
    {
        using var factory = _fixture.CreateFactory();
        INexusAuthorizationProvider firstProvider;
        IConnectionMultiplexer first;
        using (var firstScope = factory.Services.CreateScope())
        {
            firstProvider = firstScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();
            Assert.Same(firstProvider, firstScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
            first = firstScope.ServiceProvider.GetRequiredService<IConnectionMultiplexer>();
        }

        using var secondScope = factory.Services.CreateScope();
        var secondProvider = secondScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();
        var second = secondScope.ServiceProvider.GetRequiredService<IConnectionMultiplexer>();

        Assert.NotSame(firstProvider, secondProvider);
        Assert.Same(first, second);
        await second.GetDatabase().PingAsync();
    }

    [Fact]
    public async Task Go_compatible_escaped_string_key_works_without_auxiliary_keys()
    {
        const string escapedService = "demo+api%2F%E6%B5%8B%E8%AF%95%3Fx%3D1%26y%3D2";
        const string escapedDomain = "%E7%A7%9F%E6%88%B7+A%2F%E4%B8%80%E5%8F%B7";

        using var factory = _fixture.CreateFactory(
            appKey: "demo app+测试?:",
            serviceResource: "demo api/测试?x=1&y=2",
            domain: "租户 A/一号");
        var prefix = factory.Settings.Redis.KeyPrefix;
        var expectedPolicyKey =
            $"{prefix}:{escapedService}:{TestIdentity.Subject}:{escapedDomain}";

        Assert.Equal(expectedPolicyKey, _fixture.PolicyKey(factory).ToString());

        var policy = TestPolicies.Create(factory);
        await _fixture.Database.StringSetAsync(expectedPolicyKey, policy.ToJsonString());
        using var client = factory.CreateAuthenticatedClient();
        using var response = await client.GetAsync("/api/demo/orders");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        await AssertSuccessfulBodyAsync(response, "GET", "/api/demo/orders");
    }

    [Fact]
    public async Task Escaped_service_subject_and_domain_isolate_policies()
    {
        var prefix = $"nexusauthorization:integration:{Guid.NewGuid():N}";
        const string service = "订单 api/主?x=1&y=2";
        const string firstSubject = "用户 A/一号";
        const string secondSubject = "用户 B/二号";
        var unpublishedSubject = Guid.NewGuid().ToString();
        const string domain = "租户 C/东区";

        using var firstFactory = _fixture.CreateFactory(
            serviceResource: service, domain: domain, keyPrefix: prefix);
        using var secondFactory = _fixture.CreateFactory(
            serviceResource: service, domain: domain, keyPrefix: prefix);
        using var otherDomainFactory = _fixture.CreateFactory(
            serviceResource: service, domain: "租户 D/西区", keyPrefix: prefix);
        using var otherServiceFactory = _fixture.CreateFactory(
            serviceResource: "库存 api/备?z=3", domain: domain, keyPrefix: prefix);

        await _fixture.PublishAsync(
            firstFactory, TestPolicies.Create(firstFactory, firstSubject), firstSubject);
        await _fixture.PublishAsync(
            secondFactory, TestPolicies.Create(secondFactory, secondSubject), secondSubject);

        var firstExpectedKey = $"{prefix}:{RedisAuthorizationStore.QueryEscape(service)}:" +
            $"{RedisAuthorizationStore.QueryEscape(firstSubject)}:" +
            RedisAuthorizationStore.QueryEscape(domain);
        var secondExpectedKey = $"{prefix}:{RedisAuthorizationStore.QueryEscape(service)}:" +
            $"{RedisAuthorizationStore.QueryEscape(secondSubject)}:" +
            RedisAuthorizationStore.QueryEscape(domain);
        Assert.Equal(firstExpectedKey, _fixture.PolicyKey(firstFactory, firstSubject).ToString());
        Assert.Equal(secondExpectedKey, _fixture.PolicyKey(secondFactory, secondSubject).ToString());
        Assert.NotEqual(firstExpectedKey, secondExpectedKey);

        using var firstClient = firstFactory.CreateAuthenticatedClient(firstSubject);
        using var secondClient = secondFactory.CreateAuthenticatedClient(secondSubject);
        using var wrongSubjectClient =
            secondFactory.CreateAuthenticatedClient(unpublishedSubject);
        using var wrongDomainClient = otherDomainFactory.CreateAuthenticatedClient(firstSubject);
        using var wrongServiceClient = otherServiceFactory.CreateAuthenticatedClient(firstSubject);
        using var firstAllowed = await firstClient.GetAsync("/api/demo/products");
        using var secondAllowed = await secondClient.GetAsync("/api/demo/products");
        using var wrongSubject = await wrongSubjectClient.GetAsync("/api/demo/products");
        using var wrongDomain = await wrongDomainClient.GetAsync("/api/demo/products");
        using var wrongService = await wrongServiceClient.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.OK, firstAllowed.StatusCode);
        Assert.Equal(HttpStatusCode.OK, secondAllowed.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, wrongSubject.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, wrongDomain.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, wrongService.StatusCode);
    }

    [Fact]
    public async Task Same_service_permissions_do_not_depend_on_application_configuration()
    {
        var prefix = $"nexusauthorization:integration:{Guid.NewGuid():N}";
        using var withoutAppFactory = _fixture.CreateFactory(
            appKey: "", keyPrefix: prefix);
        using var legacyAppFactory = _fixture.CreateFactory(
            appKey: "legacy.application", keyPrefix: prefix);

        Assert.Equal(_fixture.PolicyKey(withoutAppFactory), _fixture.PolicyKey(legacyAppFactory));

        await _fixture.PublishAsync(withoutAppFactory, TestPolicies.Create(withoutAppFactory));
        using var withoutAppClient = withoutAppFactory.CreateAuthenticatedClient();
        using var legacyAppClient = legacyAppFactory.CreateAuthenticatedClient();
        using var withoutAppResponse = await withoutAppClient.GetAsync("/api/demo/products");
        using var legacyAppResponse = await legacyAppClient.GetAsync("/api/demo/products");

        Assert.Equal(HttpStatusCode.OK, withoutAppResponse.StatusCode);
        Assert.Equal(HttpStatusCode.OK, legacyAppResponse.StatusCode);
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
