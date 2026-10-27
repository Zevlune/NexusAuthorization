using System.Collections.Concurrent;
using System.Net;
using System.Security.Claims;
using System.Text.Json;
using System.Text.Encodings.Web;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Hosting.Server;
using Microsoft.AspNetCore.Hosting.Server.Features;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.TestHost;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using Xunit;

namespace NexusAuthorization.Sdk.IntegrationTests;

public sealed class HttpAuthorizationIntegrationTests
{
    [Fact]
    public async Task Attribute_and_real_http_provider_authorize_the_actual_authenticated_request()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient();

        using var response = await client.PostAsync("/sdk/orders/42?subject=untrusted-query", null);

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Equal(1, application.Executions.Count);
        var received = Assert.Single(application.Requests);
        Assert.Equal("POST", received.TransportMethod);
        Assert.Equal("/api/v1/authorize", received.TransportPath);
        Assert.Equal("Bearer inbound-user-token", received.Authorization);
        Assert.Equal("orders-app", received.Payload.AppKey);
        Assert.Equal("orders.api", received.Payload.ServiceResource);
        Assert.Equal("trusted-user", received.Payload.Subject);
        Assert.Equal("POST", received.Payload.Method);
        Assert.Equal("/sdk/orders/42", received.Payload.Path);
        AssertRawAuthorizationPayloadHasNoDomain(received.RawJson);
    }

    [Theory]
    [InlineData("Bearer", "token68-._~+/==")]
    [InlineData("bEaReR", "another-user-token")]
    public async Task Default_service_credential_forwards_the_current_request_bearer_token(
        string scheme, string token)
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient(
            bearerToken: token,
            authorizationScheme: scheme);

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var received = Assert.Single(application.Requests);
        Assert.Equal($"Bearer {token}", received.Authorization);
        Assert.Equal("trusted-user", received.Payload.Subject);
        Assert.Equal(1, application.Executions.Count);
    }

    [Fact]
    public async Task Explicit_service_credential_overrides_the_current_request_bearer_token()
    {
        await using var application = await HttpSdkApplication.StartAsync(
            serviceCredential: "service-only-token");
        using var client = application.CreateClient(bearerToken: "inbound-user-token");

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        var received = Assert.Single(application.Requests);
        Assert.Equal("Bearer service-only-token", received.Authorization);
        Assert.Equal(1, application.Executions.Count);
    }

    [Fact]
    public async Task UseHttp_registers_an_accessor_and_explicit_credential_supports_background_calls()
    {
        await using var application = await HttpSdkApplication.StartAsync(
            serviceCredential: "service-only-token");

        Assert.NotNull(application.BusinessServices.GetService<IHttpContextAccessor>());
        using var scope = application.BusinessServices.CreateScope();
        var provider = scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();
        var decision = await provider.AuthorizeAsync("background-user", "GET", "/background");

        Assert.Equal(new AuthorizationDecision(true, "allow", "allowed"), decision);
        var received = Assert.Single(application.Requests);
        Assert.Equal("Bearer service-only-token", received.Authorization);
        Assert.Equal("background-user", received.Payload.Subject);
        Assert.Equal("/background", received.Payload.Path);
    }

    [Fact]
    public async Task Missing_bearer_authorization_is_denied_without_an_outbound_request()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient(bearerToken: null);

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Empty(application.Requests);
        Assert.Equal(0, application.Executions.Count);
    }

    [Theory]
    [InlineData("Basic inbound-user-token")]
    [InlineData("Bearer")]
    [InlineData("Bearer token extra")]
    [InlineData("Bearer token==extra")]
    public async Task Malformed_authorization_headers_are_denied_without_an_outbound_request(
        string authorization)
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient(bearerToken: null);
        client.DefaultRequestHeaders.TryAddWithoutValidation("Authorization", authorization);

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Empty(application.Requests);
        Assert.Equal(0, application.Executions.Count);
    }

    [Fact]
    public async Task Multiple_authorization_headers_are_denied_without_an_outbound_request()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient(bearerToken: null);
        client.DefaultRequestHeaders.TryAddWithoutValidation("Authorization", "Bearer first-token");
        client.DefaultRequestHeaders.TryAddWithoutValidation("Authorization", "Bearer second-token");

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Empty(application.Requests);
        Assert.Equal(0, application.Executions.Count);
    }

    [Fact]
    public async Task Concurrent_requests_forward_each_users_own_bearer_token()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var firstClient = application.CreateClient("user-a", bearerToken: "user-a-token");
        using var secondClient = application.CreateClient("user-b", bearerToken: "user-b-token");

        var responses = await Task.WhenAll(
            firstClient.GetAsync("/sdk/orders/42"),
            secondClient.GetAsync("/sdk/orders/42"));
        using var first = responses[0];
        using var second = responses[1];

        Assert.Equal(HttpStatusCode.OK, first.StatusCode);
        Assert.Equal(HttpStatusCode.OK, second.StatusCode);
        Assert.Equal(2, application.Requests.Count);
        Assert.Contains(application.Requests, request =>
            request.Payload.Subject == "user-a" &&
            request.Authorization == "Bearer user-a-token");
        Assert.Contains(application.Requests, request =>
            request.Payload.Subject == "user-b" &&
            request.Authorization == "Bearer user-b-token");
    }

    [Fact]
    public async Task A_later_request_without_a_token_does_not_reuse_the_previous_requests_token()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var authorizedClient = application.CreateClient(bearerToken: "first-user-token");
        using var first = await authorizedClient.GetAsync("/sdk/orders/42");
        Assert.Equal(HttpStatusCode.OK, first.StatusCode);

        using var anonymousAuthorizationClient = application.CreateClient(bearerToken: null);
        using var second = await anonymousAuthorizationClient.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, second.StatusCode);
        var received = Assert.Single(application.Requests);
        Assert.Equal("Bearer first-user-token", received.Authorization);
        Assert.Equal(1, application.Executions.Count);
    }

    [Theory]
    [InlineData(false, "trusted-user", HttpStatusCode.Unauthorized)]
    [InlineData(true, null, HttpStatusCode.Forbidden)]
    public async Task Missing_authentication_or_subject_is_rejected_without_contacting_authorization_service(
        bool authenticated, string? subject, HttpStatusCode expected)
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient(subject, authenticated);

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(expected, response.StatusCode);
        Assert.Empty(application.Requests);
        Assert.Equal(0, application.Executions.Count);
    }

    [Theory]
    [InlineData(200, "{\"allowed\":false,\"effect\":\"deny\",\"reason\":\"endpoint_not_granted\"}")]
    [InlineData(200, "{\"allowed\":true,\"effect\":\"deny\",\"reason\":\"allowed\"}")]
    [InlineData(200, "{\"allowed\":true}")]
    [InlineData(200, "not-json")]
    [InlineData(201, "{\"allowed\":true,\"effect\":\"allow\",\"reason\":\"allowed\"}")]
    [InlineData(401, "{\"allowed\":true,\"effect\":\"allow\",\"reason\":\"allowed\"}")]
    [InlineData(503, "{\"allowed\":true,\"effect\":\"allow\",\"reason\":\"allowed\"}")]
    public async Task Denial_or_invalid_remote_response_never_executes_the_business_action(int status, string body)
    {
        await using var application = await HttpSdkApplication.StartAsync();
        application.Respond = context =>
        {
            context.Response.StatusCode = status;
            context.Response.ContentType = "application/json";
            return context.Response.WriteAsync(body, context.RequestAborted);
        };
        using var client = application.CreateClient();

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Single(application.Requests);
        Assert.Equal(0, application.Executions.Count);
    }

    [Fact]
    public async Task Revocation_is_observed_on_the_next_request_without_cached_allow()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient();
        using var first = await client.GetAsync("/sdk/orders/42");
        Assert.Equal(HttpStatusCode.OK, first.StatusCode);
        application.Respond = context => context.Response.WriteAsJsonAsync(
            new AuthorizationDecision(false, "deny", "endpoint_not_granted"), context.RequestAborted);

        using var revoked = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, revoked.StatusCode);
        Assert.Equal(2, application.Requests.Count);
        Assert.Equal(1, application.Executions.Count);
    }

    [Fact]
    public async Task Concurrent_subjects_use_independent_remote_decisions_and_outbound_payloads_have_no_domain()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        application.Respond = context =>
        {
            var input = (AuthorizationWireRequest)context.Items[typeof(AuthorizationWireRequest)]!;
            var allowed = input.Subject == "allowed-user";
            return context.Response.WriteAsJsonAsync(new AuthorizationDecision(allowed,
                allowed ? "allow" : "deny", allowed ? "allowed" : "endpoint_not_granted"), context.RequestAborted);
        };
        using var allowedClient = application.CreateClient("allowed-user");
        using var deniedClient = application.CreateClient("denied-user");

        var requests = await Task.WhenAll(allowedClient.GetAsync("/sdk/orders/42"), deniedClient.GetAsync("/sdk/orders/42"));
        using var allowed = requests[0];
        using var denied = requests[1];

        Assert.Equal(HttpStatusCode.OK, allowed.StatusCode);
        Assert.Equal(HttpStatusCode.Forbidden, denied.StatusCode);
        Assert.Equal(1, application.Executions.Count);
        Assert.Equal(2, application.Requests.Count);
        Assert.All(application.Requests, input =>
            AssertRawAuthorizationPayloadHasNoDomain(input.RawJson));
        Assert.Equal(new[] { "allowed-user", "denied-user" },
            application.Requests.Select(input => input.Payload.Subject).Order().ToArray());
    }

    [Fact]
    public async Task Remote_redirect_is_not_followed_even_if_the_target_would_allow()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        application.Respond = context =>
        {
            context.Response.StatusCode = StatusCodes.Status307TemporaryRedirect;
            context.Response.Headers.Location = "/redirect-allow";
            return Task.CompletedTask;
        };
        using var client = application.CreateClient();

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Equal(0, application.RedirectRequests);
        Assert.Equal(0, application.Executions.Count);
    }

    [Fact]
    public async Task Remote_timeout_fails_closed_and_cancels_the_outbound_request()
    {
        await using var application = await HttpSdkApplication.StartAsync(timeoutMilliseconds: 1000);
        var reachedService = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        var requestCancelled = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        application.Respond = async context =>
        {
            reachedService.TrySetResult();
            try { await Task.Delay(Timeout.InfiniteTimeSpan, context.RequestAborted); }
            catch (OperationCanceledException) { requestCancelled.TrySetResult(); }
        };
        using var client = application.CreateClient();

        var request = client.GetAsync("/sdk/orders/42");
        await reachedService.Task.WaitAsync(TimeSpan.FromSeconds(5));
        using var response = await request;

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Equal(0, application.Executions.Count);
        await requestCancelled.Task.WaitAsync(TimeSpan.FromSeconds(5));
    }

    [Fact]
    public async Task Chunked_response_above_the_limit_is_denied_before_business_execution()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        application.Respond = async context =>
        {
            context.Response.ContentType = "application/json";
            await context.Response.StartAsync(context.RequestAborted);
            await context.Response.WriteAsync("{\"allowed\":true,\"effect\":\"allow\",\"reason\":\"allowed\",\"padding\":\"", context.RequestAborted);
            await context.Response.WriteAsync(new string('x', 1024 * 1024), context.RequestAborted);
            await context.Response.WriteAsync("\"}", context.RequestAborted);
        };
        using var client = application.CreateClient();

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Equal(0, application.Executions.Count);
    }

    [Fact]
    public async Task Unavailable_authorization_service_does_not_allow_business_requests()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        await application.StopAuthorizationServiceAsync();
        using var client = application.CreateClient();

        using var response = await client.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
        Assert.Equal(0, application.Executions.Count);
    }

    [Fact]
    public async Task Authorization_response_cookies_are_not_reused_for_another_subject()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        application.Respond = context =>
        {
            context.Response.Headers.Append("Set-Cookie", "authorization-session=subject-a; Path=/");
            return HttpSdkApplication.AllowAsync(context);
        };
        using var firstClient = application.CreateClient("user-a");
        using var secondClient = application.CreateClient("user-b");

        using var first = await firstClient.GetAsync("/sdk/orders/42");
        using var second = await secondClient.GetAsync("/sdk/orders/42");

        Assert.Equal(HttpStatusCode.OK, first.StatusCode);
        Assert.Equal(HttpStatusCode.OK, second.StatusCode);
        Assert.Equal(2, application.Requests.Count);
        Assert.All(application.Requests, request => Assert.Empty(request.Cookie));
    }

    [Fact]
    public async Task AllowAnonymous_bypasses_the_permission_attribute_and_remote_service()
    {
        await using var application = await HttpSdkApplication.StartAsync();
        using var client = application.CreateClient(authenticated: false);

        using var response = await client.GetAsync("/sdk/public");

        Assert.Equal(HttpStatusCode.OK, response.StatusCode);
        Assert.Empty(application.Requests);
        Assert.Equal(0, application.Executions.Count);
    }

    private static void AssertRawAuthorizationPayloadHasNoDomain(string rawJson)
    {
        using var document = JsonDocument.Parse(rawJson);
        Assert.DoesNotContain(document.RootElement.EnumerateObject(), property =>
            string.Equals(property.Name, "domain", StringComparison.OrdinalIgnoreCase));
    }
}

// 入站业务请求使用 TestServer；SDK 的出站请求通过真实 socket 访问临时 Kestrel 授权服务。
internal sealed class HttpSdkApplication : IAsyncDisposable
{
    private WebApplication authorization = null!;
    private IHost business = null!;
    private int redirectRequests;
    public ConcurrentQueue<ReceivedAuthorizationRequest> Requests { get; } = new();
    public BusinessExecutions Executions { get; } = new();
    public Func<HttpContext, Task> Respond { get; set; } = AllowAsync;
    public int RedirectRequests => Volatile.Read(ref redirectRequests);
    public IServiceProvider BusinessServices => business.Services;

    public static Task AllowAsync(HttpContext context) => context.Response.WriteAsJsonAsync(
        new AuthorizationDecision(true, "allow", "allowed"), context.RequestAborted);

    public static async Task<HttpSdkApplication> StartAsync(
        int timeoutMilliseconds = 3000,
        string? serviceCredential = null)
    {
        var application = new HttpSdkApplication();
        try
        {
            var builder = WebApplication.CreateBuilder(new WebApplicationOptions { EnvironmentName = "Testing" });
            builder.Logging.ClearProviders();
            builder.WebHost.ConfigureKestrel(options => options.Listen(IPAddress.Loopback, 0));
            application.authorization = builder.Build();
            application.authorization.MapPost("/api/v1/authorize", async context =>
            {
                using var reader = new StreamReader(context.Request.Body);
                var rawJson = await reader.ReadToEndAsync(context.RequestAborted);
                var payload = JsonSerializer.Deserialize<AuthorizationWireRequest>(
                        rawJson,
                        new JsonSerializerOptions(JsonSerializerDefaults.Web))
                    ?? throw new InvalidOperationException("Expected authorization JSON.");
                application.Requests.Enqueue(new(context.Request.Method, context.Request.Path,
                    context.Request.Headers.Authorization.ToString(), context.Request.Headers.Cookie.ToString(),
                    rawJson, payload));
                context.Items[typeof(AuthorizationWireRequest)] = payload;
                try { await application.Respond(context); }
                catch (OperationCanceledException) when (context.RequestAborted.IsCancellationRequested) { }
                catch (IOException) when (context.RequestAborted.IsCancellationRequested) { }
            });
            application.authorization.MapGet("/redirect-allow", async context =>
            {
                Interlocked.Increment(ref application.redirectRequests);
                await AllowAsync(context);
            });
            await application.authorization.StartAsync();
            var address = application.authorization.Services.GetRequiredService<IServer>()
                .Features.Get<IServerAddressesFeature>()!.Addresses.Single();
            application.business = await new HostBuilder().ConfigureWebHost(web => web
                .UseTestServer()
                .ConfigureLogging(logging => logging.ClearProviders())
                .ConfigureServices(services =>
                {
                    services.AddSingleton(application.Executions);
                    services.AddAuthentication(TestIdentityHandler.SchemeName)
                        .AddScheme<AuthenticationSchemeOptions, TestIdentityHandler>(TestIdentityHandler.SchemeName, _ => { });
                    services.AddNexusAuthorization(options =>
                    {
                        options.ServiceResource = " orders.api ";
                    }).UseHttp(options =>
                    {
                        options.BaseUrl = address;
                        options.AppKey = " orders-app ";
                        options.ServiceCredential = serviceCredential ?? string.Empty;
                        options.TimeoutMilliseconds = timeoutMilliseconds;
                    });
                    services.AddControllers().AddApplicationPart(typeof(SdkProbeController).Assembly);
                })
                .Configure(app =>
                {
                    app.UseRouting();
                    app.UseAuthentication();
                    app.UseAuthorization();
                    app.UseEndpoints(endpoints => endpoints.MapControllers());
                })).StartAsync();
            return application;
        }
        catch
        {
            await application.DisposeAsync();
            throw;
        }
    }

    public HttpClient CreateClient(
        string? subject = "trusted-user",
        bool authenticated = true,
        string? bearerToken = "inbound-user-token",
        string authorizationScheme = "Bearer")
    {
        var client = business.GetTestClient();
        client.Timeout = TimeSpan.FromSeconds(10);
        if (authenticated) client.DefaultRequestHeaders.Add("X-Test-Authenticated", "true");
        if (subject is not null) client.DefaultRequestHeaders.Add("X-Test-Subject", subject);
        if (bearerToken is not null)
        {
            client.DefaultRequestHeaders.TryAddWithoutValidation(
                "Authorization",
                $"{authorizationScheme} {bearerToken}");
        }
        return client;
    }

    public Task StopAuthorizationServiceAsync() => authorization.StopAsync();

    public async ValueTask DisposeAsync()
    {
        if (business is not null)
        {
            await business.StopAsync();
            business.Dispose();
        }
        if (authorization is not null)
            await authorization.DisposeAsync();
    }
}

internal sealed record AuthorizationWireRequest(string AppKey, string ServiceResource, string Subject,
    string Method, string Path);
internal sealed record ReceivedAuthorizationRequest(string TransportMethod, string TransportPath,
    string Authorization, string Cookie, string RawJson, AuthorizationWireRequest Payload);

public sealed class BusinessExecutions
{
    private int count;
    public int Count => Volatile.Read(ref count);
    public void Record() => Interlocked.Increment(ref count);
}

[ApiController]
[NexusAuthorize]
public sealed class SdkProbeController(BusinessExecutions executions) : ControllerBase
{
    [HttpGet("/sdk/orders/{id:int}")]
    [HttpPost("/sdk/orders/{id:int}")]
    public IActionResult Order(int id)
    {
        executions.Record();
        return Ok(new { id });
    }

    [HttpGet("/sdk/public")]
    [AllowAnonymous]
    public IActionResult Public() => Ok(new { available = true });
}

internal sealed class TestIdentityHandler(IOptionsMonitor<AuthenticationSchemeOptions> options,
    ILoggerFactory logger, UrlEncoder encoder) : AuthenticationHandler<AuthenticationSchemeOptions>(options, logger, encoder)
{
    public const string SchemeName = "SdkIntegration";

    protected override Task<AuthenticateResult> HandleAuthenticateAsync()
    {
        if (Request.Headers["X-Test-Authenticated"] != "true")
            return Task.FromResult(AuthenticateResult.NoResult());
        var subject = Request.Headers["X-Test-Subject"].ToString();
        var claims = subject.Length == 0 ? Array.Empty<Claim>() : [new Claim("sub", subject)];
        var principal = new ClaimsPrincipal(new ClaimsIdentity(claims, SchemeName));
        return Task.FromResult(AuthenticateResult.Success(new AuthenticationTicket(principal, SchemeName)));
    }
}
