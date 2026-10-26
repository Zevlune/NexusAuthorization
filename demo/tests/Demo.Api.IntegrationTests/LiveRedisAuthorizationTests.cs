using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Redis;
using System.Net;
using System.Net.Http.Json;
using System.Text.Json;
using Demo.Api;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using StackExchange.Redis;
using Xunit.Abstractions;

namespace Demo.Api.IntegrationTests;

// 现网 Redis 只执行 GET / STRING GET，绝不发布、清理或改写授权数据。
// POST/PUT/DELETE 仅调用 TestHost 内的 Controller mock，不连接数据库或持久化。
// 现网 Redis 必须先部署新 String 协议；未部署时保持测试禁用。
[Trait("Category", "LiveRedis")]
public sealed class LiveRedisAuthorizationTests(LiveRedisFixture fixture, ITestOutputHelper output)
    : IClassFixture<LiveRedisFixture>
{
    [LiveRedisFact]
    public async Task Published_string_policy_matches_the_current_user_and_service()
    {
        var profile = fixture.Profile;
        var settings = fixture.Factory.Settings.AuthorizationCenter;
        Assert.Equal(profile.ServiceResource, settings.ServiceResource);
        Assert.Equal(profile.Domain, RedisAuthorizationStore.NormalizeDomain(settings.Domain));

        var connection = fixture.Factory.Services.GetRequiredService<IConnectionMultiplexer>();
        var database = connection.GetDatabase(fixture.Factory.Settings.Redis.Database);
        var raw = await database.StringGetAsync(fixture.PolicyKey);
        Assert.False(raw.IsNull, $"用户授权 String 尚未发布：{fixture.PolicyKey}");
        var policy = JsonSerializer.Deserialize<SubjectApiPolicy>(raw.ToString());
        Assert.NotNull(policy);
        Assert.Equal(profile.Subject, policy.Subject);
        Assert.Equal(profile.ServiceResource, policy.ServiceResource);
        Assert.Equal(profile.Domain, RedisAuthorizationStore.NormalizeDomain(policy.Domain));
        Assert.True(policy.ServiceActive);
        Assert.Contains(profile.ExpectedRole, policy.Roles ?? []);
        output.WriteLine($"已核对：{fixture.PolicyKey}，角色={profile.ExpectedRole}。");
    }

    [LiveRedisTheory]
    [MemberData(nameof(DemoEndpoints.Cases), MemberType = typeof(DemoEndpoints))]
    public async Task Existing_api_matches_the_published_user_permissions(
        string method, string path, string template, int successStatus)
    {
        var profile = fixture.Profile;
        var operation = $"{method} {path}";
        var allowed = profile.AllowedEndpoints.Contains(operation, StringComparer.Ordinal);
        Assert.True(allowed || profile.DeniedReasons.ContainsKey(operation),
            $"请为 {operation} 在 live-authorization.json 中填写明确的预期。");

        using var client = fixture.Factory.CreateAuthenticatedClient(profile.Subject);
        using var request = new HttpRequestMessage(new HttpMethod(method), path);
        if (method is "POST" or "PUT" && path.StartsWith("/api/demo/products", StringComparison.Ordinal))
            request.Content = JsonContent.Create(new { name = "集成测试商品", price = 49.90m });
        using var response = await client.SendAsync(request);
        Assert.Equal(allowed ? successStatus : 403, (int)response.StatusCode);

        using var scope = fixture.Factory.Services.CreateScope();
        var store = scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();
        var decision = await store.AuthorizeAsync(profile.Subject, method, path, CancellationToken.None);
        Assert.Equal(allowed, decision.Allowed);
        Assert.Equal(allowed ? "allow" : "deny", decision.Effect);
        Assert.Equal(allowed ? "allowed" : profile.DeniedReasons[operation], decision.Reason);
        output.WriteLine($"{operation}（{template}）=> HTTP {(int)response.StatusCode}，{decision.Reason}；sub={profile.Subject}");
    }

    [LiveRedisFact]
    public async Task Another_subject_cannot_use_the_current_users_order_grant()
    {
        using var client = fixture.Factory.CreateAuthenticatedClient(Guid.NewGuid().ToString());
        using var response = await client.GetAsync("/api/demo/orders");
        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [LiveRedisFact]
    public async Task Anonymous_request_cannot_use_the_published_order_grant()
    {
        using var client = fixture.Factory.CreateClient();
        using var response = await client.GetAsync("/api/demo/orders");
        Assert.Equal(HttpStatusCode.Unauthorized, response.StatusCode);
    }

}

public sealed class LiveRedisFixture : IAsyncLifetime
{
    public static bool Enabled => Environment.GetEnvironmentVariable("NEXUS_DEMO_RUN_LIVE_TESTS") == "1";
    public AuthorizationApiFactory Factory { get; private set; } = null!;
    public LiveAuthorizationProfile Profile { get; private set; } = null!;

    public string PolicyKey
    {
        get
        {
            var key = $"{Factory.Settings.Redis.KeyPrefix}:" +
                $"{RedisAuthorizationStore.QueryEscape(Profile.ServiceResource.Trim())}:" +
                RedisAuthorizationStore.QueryEscape(Profile.Subject.Trim());
            var domain = RedisAuthorizationStore.NormalizeDomain(Profile.Domain);
            return domain.Length == 0
                ? key
                : $"{key}:{RedisAuthorizationStore.QueryEscape(domain)}";
        }
    }

    public Task InitializeAsync()
    {
        if (!Enabled)
            return Task.CompletedTask;

        var profileFile = Environment.GetEnvironmentVariable("NEXUS_DEMO_LIVE_EXPECTATIONS") ??
            Path.Combine(AppContext.BaseDirectory, "live-authorization.json");
        Profile = JsonSerializer.Deserialize<LiveAuthorizationProfile>(File.ReadAllText(profileFile)) ??
            throw new InvalidOperationException("现网授权测试预期配置为空。");
        if (string.IsNullOrWhiteSpace(Profile.Subject) || Profile.AllowedEndpoints.Count == 0)
            throw new InvalidOperationException("现网测试必须指定用户 ID 和至少一个预期放行接口，避免全部拒绝时误报成功。");

        var configuration = new ConfigurationBuilder()
            .AddJsonFile(Path.Combine(DemoProjectPaths.ApiDirectory, "appsettings.json"))
            .AddUserSecrets<Program>(optional: true)
            .AddEnvironmentVariables()
            .Build();
        var settings = configuration.Get<DemoSettings>() ?? throw new InvalidOperationException("缺少 Demo 配置。");
        Factory = new AuthorizationApiFactory(settings);
        return Task.CompletedTask;
    }

    public async Task DisposeAsync()
    {
        if (Factory is not null)
            await Factory.DisposeAsync();
    }
}

public sealed class LiveAuthorizationProfile
{
    public string Subject { get; init; } = "";
    public string ServiceResource { get; init; } = "";
    public string Domain { get; init; } = "";
    public string ExpectedRole { get; init; } = "";
    public List<string> AllowedEndpoints { get; init; } = [];
    public Dictionary<string, string> DeniedReasons { get; init; } = [];
}

public sealed class LiveRedisFactAttribute : FactAttribute
{
    public LiveRedisFactAttribute()
    {
        if (!LiveRedisFixture.Enabled)
            Skip = "需先部署新 String 协议；再设置 NEXUS_DEMO_RUN_LIVE_TESTS=1，使用当前 Redis 与用户 ID 执行只读 GET 验证。";
    }
}

public sealed class LiveRedisTheoryAttribute : TheoryAttribute
{
    public LiveRedisTheoryAttribute()
    {
        if (!LiveRedisFixture.Enabled)
            Skip = "需先部署新 String 协议；再设置 NEXUS_DEMO_RUN_LIVE_TESTS=1，使用当前 Redis 与用户 ID 执行只读 GET 验证。";
    }
}
