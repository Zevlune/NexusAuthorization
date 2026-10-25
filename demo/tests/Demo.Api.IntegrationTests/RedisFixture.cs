using System.Diagnostics;
using System.Text.Json.Nodes;
using Demo.Api;
using StackExchange.Redis;

namespace Demo.Api.IntegrationTests;

// 测试数据只写入此 fixture 自己启动的临时容器；不接收业务 Redis 连接参数。
public sealed class RedisFixture : IAsyncLifetime
{
    private readonly string containerName = $"nexus-demo-tests-{Guid.NewGuid():N}";
    private ConnectionMultiplexer? connection;
    private int port;
    private bool containerCreated;

    public IDatabase Database => (connection ?? throw new InvalidOperationException("Redis 尚未启动。")).GetDatabase();

    public async Task InitializeAsync()
    {
        try
        {
            await DockerAsync("run", "--rm", "-d", "--name", containerName,
                "-p", "127.0.0.1::6379", "redis:7-alpine", "redis-server", "--save", "", "--appendonly", "no");
            containerCreated = true;
            var mapping = await DockerAsync("port", containerName, "6379/tcp");
            port = int.Parse(mapping.Trim().Split(':')[^1]);
            connection = await ConnectionMultiplexer.ConnectAsync(new ConfigurationOptions
            {
                EndPoints = { { "127.0.0.1", port } },
                AbortOnConnectFail = true,
                ConnectTimeout = 5000,
                ConnectRetry = 2,
                AllowAdmin = false
            });
            await Database.PingAsync();
        }
        catch
        {
            await DisposeAsync();
            throw;
        }
    }

    public AuthorizationApiFactory CreateFactory(string appKey = "demo",
        string serviceResource = "nexusauthorization.demo.api", string domain = "",
        string? keyPrefix = null) => new(new DemoSettings
    {
        AuthorizationCenter = new AuthorizationCenterSettings
        {
            AppKey = appKey, ServiceResource = serviceResource, Domain = domain
        },
        Redis = new RedisSettings
        {
            Host = "127.0.0.1", Port = port,
            KeyPrefix = keyPrefix ?? $"nexusauthorization:integration:{Guid.NewGuid():N}",
            TimeoutMilliseconds = 3000
        }
    });

    public RedisKey StateKey(AuthorizationApiFactory factory) =>
        $"{factory.Settings.Redis.KeyPrefix}:rbac:api:app:" +
        $"{RedisAuthorizationStore.QueryEscape(factory.Settings.AuthorizationCenter.AppKey.Trim())}:state";

    public RedisKey PolicyKey(AuthorizationApiFactory factory, string? subject = null)
    {
        var settings = factory.Settings;
        var scope = settings.AuthorizationCenter;
        var key = $"{settings.Redis.KeyPrefix}:{RedisAuthorizationStore.QueryEscape(scope.ServiceResource.Trim())}:" +
            RedisAuthorizationStore.QueryEscape((subject ?? TestIdentity.Subject).Trim());
        var domain = RedisAuthorizationStore.NormalizeDomain(scope.Domain);
        return domain.Length == 0 ? key : $"{key}:{RedisAuthorizationStore.QueryEscape(domain)}";
    }

    public RedisValue PolicyField(AuthorizationApiFactory factory) =>
        factory.Settings.AuthorizationCenter.AppKey.Trim();

    public async Task PublishAsync(AuthorizationApiFactory factory, JsonObject policy,
        string? subject = null, string state = "ready")
    {
        var transaction = Database.CreateTransaction();
        var fieldAppKey = PolicyField(factory);
        var record = transaction.HashSetAsync(
            PolicyKey(factory, subject), fieldAppKey, policy.ToJsonString());
        var gate = transaction.StringSetAsync(StateKey(factory), state);
        if (!await transaction.ExecuteAsync())
            throw new InvalidOperationException("测试授权发布失败。");
        await Task.WhenAll(record, gate);
    }

    public async Task DisposeAsync()
    {
        if (connection is not null)
        {
            await connection.DisposeAsync();
            connection = null;
        }
        if (containerCreated)
        {
            await DockerAsync("rm", "--force", containerName);
            containerCreated = false;
        }
    }

    private static async Task<string> DockerAsync(params string[] arguments)
    {
        var info = new ProcessStartInfo("docker")
        {
            RedirectStandardOutput = true, RedirectStandardError = true, UseShellExecute = false
        };
        foreach (var argument in arguments)
            info.ArgumentList.Add(argument);
        using var process = Process.Start(info) ?? throw new InvalidOperationException("无法启动 Docker，请确认 Docker 已运行。");
        var stdout = process.StandardOutput.ReadToEndAsync();
        var stderr = process.StandardError.ReadToEndAsync();
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(45));
        try { await process.WaitForExitAsync(timeout.Token); }
        catch (OperationCanceledException)
        {
            process.Kill(entireProcessTree: true);
            throw new TimeoutException("启动或清理测试 Redis 超时，请检查 Docker。");
        }
        var output = await stdout;
        var error = await stderr;
        if (process.ExitCode != 0)
            throw new InvalidOperationException($"测试 Redis 的 Docker 操作失败：{error.Trim()}");
        return output;
    }
}

internal static class TestPolicies
{
    public static JsonObject Create(AuthorizationApiFactory factory, string subject = TestIdentity.Subject)
    {
        var scope = factory.Settings.AuthorizationCenter;
        var catalog = new JsonArray();
        foreach (var endpoint in DemoEndpoints.All)
            catalog.Add(new JsonObject
            {
                ["id"] = Guid.NewGuid().ToString(), ["method"] = endpoint.Method,
                ["path_template"] = endpoint.Template, ["enabled"] = true
            });
        return new JsonObject
        {
            ["application_key"] = scope.AppKey.Trim(), ["subject"] = subject.Trim(),
            ["service_resource"] = scope.ServiceResource.Trim(),
            ["domain"] = RedisAuthorizationStore.NormalizeDomain(scope.Domain),
            ["service_active"] = true, ["roles"] = new JsonArray("integration-test"),
            ["catalog"] = catalog, ["apis"] = catalog.DeepClone()
        };
    }
}
