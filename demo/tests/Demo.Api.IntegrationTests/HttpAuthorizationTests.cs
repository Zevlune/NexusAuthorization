using System.Collections.Concurrent;
using System.Net;
using System.Net.Http.Headers;
using System.Net.Http.Json;
using System.Text.Json;
using Demo.Api;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Logging;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using StackExchange.Redis;
using IServer = Microsoft.AspNetCore.Hosting.Server.IServer;

namespace Demo.Api.IntegrationTests;

public sealed class HttpAuthorizationTests
{
    [Theory]
    [MemberData(nameof(DemoEndpoints.Cases), MemberType = typeof(DemoEndpoints))]
    public async Task Http_mode_authorizes_each_real_controller_action_and_forwards_bearer(
        string method, string path, string _, int successStatus)
    {
        await using var server = await DemoAuthorizationServer.StartAsync();
        using var factory = server.CreateFactory(provider: " hTtP ");
        using var client = factory.CreateAuthenticatedClient();
        client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Bearer", "current-user-token");
        using var request = new HttpRequestMessage(new HttpMethod(method), path + "?subject=untrusted");
        if (method is "POST" or "PUT" && path.StartsWith("/api/demo/products", StringComparison.Ordinal))
            request.Content = JsonContent.Create(new { name = "HTTP 测试商品", price = 49.90m });

        using var response = await client.SendAsync(request);

        Assert.Equal(successStatus, (int)response.StatusCode);
        var received = Assert.Single(server.Requests);
        Assert.Equal("Bearer current-user-token", received.Authorization);
        Assert.Equal("demo-http-app", received.Payload.AppKey);
        Assert.Equal("nexusauthorization.demo.api", received.Payload.ServiceResource);
        Assert.Equal(TestIdentity.Subject, received.Payload.Subject);
        Assert.Equal(method, received.Payload.Method);
        Assert.Equal(path, received.Payload.Path);
        AssertRawAuthorizationPayloadHasNoDomain(received.RawJson);
        using var scope = factory.Services.CreateScope();
        Assert.IsType<HttpAuthorizationProvider>(scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        Assert.Null(factory.Services.GetService<IConnectionMultiplexer>());
    }

    [Fact]
    public async Task Fixed_credential_supports_authenticated_requests_without_inbound_bearer()
    {
        await using var server = await DemoAuthorizationServer.StartAsync();
        using var factory = server.CreateFactory(serviceCredential: "configured-service-token");
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/orders");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal("Bearer configured-service-token", Assert.Single(server.Requests).Authorization);
    }

    [Fact]
    public async Task Missing_bearer_is_denied_without_calling_the_remote_service()
    {
        await using var server = await DemoAuthorizationServer.StartAsync();
        using var factory = server.CreateFactory();
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/orders");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Empty(server.Requests);
    }

    [Fact]
    public async Task Remote_revocation_takes_effect_on_the_next_demo_request()
    {
        await using var server = await DemoAuthorizationServer.StartAsync();
        using var factory = server.CreateFactory(serviceCredential: "configured-service-token");
        using var client = factory.CreateAuthenticatedClient();
        using var allowed = await client.GetAsync("/api/demo/orders");
        Assert.Equal(HttpStatusCode.OK, allowed.StatusCode);
        server.Decision = new AuthorizationDecision(false, "deny", "endpoint_not_granted");

        using var denied = await client.GetAsync("/api/demo/orders");

        Assert.Equal(HttpStatusCode.Forbidden, denied.StatusCode);
        Assert.Equal(2, server.Requests.Count);
    }

    [Theory]
    [InlineData("/api/public/ping", false)]
    [InlineData("/health", false)]
    [InlineData("/api/auth/me", true)]
    [InlineData("/api/demo/orders", false)]
    public async Task Anonymous_and_authentication_only_endpoints_do_not_call_the_provider(
        string path, bool authenticated)
    {
        await using var server = await DemoAuthorizationServer.StartAsync();
        using var factory = server.CreateFactory(serviceCredential: "configured-service-token");
        using var client = authenticated ? factory.CreateAuthenticatedClient() : factory.CreateClient();

        using var response = await client.GetAsync(path);

        Assert.Equal(path == "/api/demo/orders" ? HttpStatusCode.Unauthorized : HttpStatusCode.OK,
            response.StatusCode);
        Assert.Empty(server.Requests);
    }

    [Theory]
    [InlineData("Grpc")]
    [InlineData("")]
    public void Unknown_provider_fails_at_startup(string provider)
    {
        using var factory = new AuthorizationApiFactory(new DemoSettings
        {
            AuthorizationCenter = new AuthorizationCenterSettings
            {
                Provider = provider, ServiceResource = "nexusauthorization.demo.api"
            }
        });

        var error = Assert.Throws<InvalidOperationException>(() => factory.CreateClient());

        Assert.Contains("AuthorizationCenter:Provider", error.Message);
    }

    private static void AssertRawAuthorizationPayloadHasNoDomain(string rawJson)
    {
        using var document = JsonDocument.Parse(rawJson);
        Assert.DoesNotContain(document.RootElement.EnumerateObject(), property =>
            string.Equals(property.Name, "domain", StringComparison.OrdinalIgnoreCase));
    }
}

// 临时本机授权服务，Demo 的出站请求使用真实 SDK HttpClient 和 socket。
internal sealed class DemoAuthorizationServer(WebApplication application) : IAsyncDisposable
{
    public ConcurrentQueue<DemoAuthorizationRequest> Requests { get; } = new();
    public AuthorizationDecision Decision { get; set; } = new(true, "allow", "allowed");

    public static async Task<DemoAuthorizationServer> StartAsync()
    {
        var builder = WebApplication.CreateBuilder(new WebApplicationOptions { EnvironmentName = "Testing" });
        builder.Logging.ClearProviders();
        builder.WebHost.ConfigureKestrel(options => options.Listen(IPAddress.Loopback, 0));
        var application = builder.Build();
        var server = new DemoAuthorizationServer(application);
        application.MapPost("/api/v1/authorize", async context =>
        {
            using var reader = new StreamReader(context.Request.Body);
            var rawJson = await reader.ReadToEndAsync(context.RequestAborted);
            var payload = JsonSerializer.Deserialize<DemoAuthorizationPayload>(
                    rawJson,
                    new JsonSerializerOptions(JsonSerializerDefaults.Web))
                ?? throw new InvalidOperationException("缺少授权请求。");
            server.Requests.Enqueue(new(context.Request.Headers.Authorization.ToString(), rawJson, payload));
            await context.Response.WriteAsJsonAsync(server.Decision, context.RequestAborted);
        });
        try
        {
            await application.StartAsync();
            return server;
        }
        catch
        {
            await application.DisposeAsync();
            throw;
        }
    }

    public AuthorizationApiFactory CreateFactory(string provider = "Http", string serviceCredential = "")
    {
        var address = application.Services.GetRequiredService<IServer>()
            .Features.Get<IServerAddressesFeature>()!.Addresses.Single();
        return new AuthorizationApiFactory(new DemoSettings
        {
            AuthorizationCenter = new AuthorizationCenterSettings
            {
                Provider = provider, AppKey = "demo-http-app",
                ServiceResource = "nexusauthorization.demo.api"
            },
            Http = new HttpAuthorizationOptions
            {
                BaseUrl = address, ServiceCredential = serviceCredential, TimeoutMilliseconds = 3000
            }
        });
    }

    public ValueTask DisposeAsync() => application.DisposeAsync();
}

internal sealed record DemoAuthorizationPayload(string AppKey, string ServiceResource, string Subject,
    string Method, string Path);
internal sealed record DemoAuthorizationRequest(string Authorization, string RawJson,
    DemoAuthorizationPayload Payload);
