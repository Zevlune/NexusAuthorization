using System.Security.Claims;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using NexusAuthorization.Sdk;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class AspNetCoreAuthorizationTests
{
    [Fact]
    public async Task Policy_uses_the_configured_subject_claim_and_actual_http_request()
    {
        var checks = new RecordingProvider(new(true, "allow", "allowed"));
        using var services = CreateServices(checks);
        using var scope = services.CreateScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var principal = new ClaimsPrincipal(new ClaimsIdentity(
            [new Claim("uid", "user-42"), new Claim("sub", "wrong-subject")], "Test"));
        var http = new DefaultHttpContext();
        http.Request.Method = "POST";
        http.Request.Path = "/api/orders/42";
        using var abort = new CancellationTokenSource();
        http.RequestAborted = abort.Token;

        var result = await authorization.AuthorizeAsync(principal, http, NexusAuthorizationDefaults.PolicyName);

        Assert.True(result.Succeeded);
        Assert.Equal(("user-42", "POST", "/api/orders/42", abort.Token), checks.LastRequest);
        Assert.Equal(NexusAuthorizationDefaults.PolicyName, new NexusAuthorizeAttribute().Policy);
    }

    [Theory]
    [InlineData(false, "deny", "endpoint_not_granted")]
    [InlineData(true, "deny", "allowed")]
    [InlineData(true, "allow", "unexpected")]
    public async Task Policy_rejects_denied_and_inconsistent_decisions(bool allowed, string effect, string reason)
    {
        using var services = CreateServices(new RecordingProvider(new(allowed, effect, reason)));
        using var scope = services.CreateScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var principal = new ClaimsPrincipal(new ClaimsIdentity([new Claim("uid", "user-42")], "Test"));

        var result = await authorization.AuthorizeAsync(principal, new DefaultHttpContext(),
            NexusAuthorizationDefaults.PolicyName);

        Assert.False(result.Succeeded);
    }

    [Theory]
    [InlineData(false, true)]
    [InlineData(true, false)]
    public async Task Policy_does_not_call_provider_for_unauthenticated_or_missing_subject(
        bool authenticated, bool withSubject)
    {
        var checks = new RecordingProvider(new(true, "allow", "allowed"));
        using var services = CreateServices(checks);
        using var scope = services.CreateScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var claims = withSubject ? new[] { new Claim("uid", "user-42") } : [];
        var principal = new ClaimsPrincipal(new ClaimsIdentity(claims, authenticated ? "Test" : null));

        var result = await authorization.AuthorizeAsync(principal, new DefaultHttpContext(),
            NexusAuthorizationDefaults.PolicyName);

        Assert.False(result.Succeeded);
        Assert.Null(checks.LastRequest);
    }

    private static ServiceProvider CreateServices(RecordingProvider checks)
    {
        var services = new ServiceCollection();
        services.AddNexusAuthorization(options =>
        {
            options.ServiceResource = "orders.api";
            options.SubjectClaimType = "uid";
        }).UseProvider(checks);
        return services.BuildServiceProvider(new ServiceProviderOptions { ValidateScopes = true });
    }

    private sealed class RecordingProvider(AuthorizationDecision decision) : INexusAuthorizationProvider
    {
        public (string Subject, string Method, string Path, CancellationToken Token)? LastRequest { get; private set; }

        public Task<AuthorizationDecision> AuthorizeAsync(string subject, string method, string path,
            CancellationToken cancellationToken = default)
        {
            LastRequest = (subject, method, path, cancellationToken);
            return Task.FromResult(decision);
        }
    }
}
