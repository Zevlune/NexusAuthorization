using System.Security.Claims;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.Mvc.Abstractions;
using Microsoft.AspNetCore.Mvc.Filters;
using Microsoft.AspNetCore.Routing;
using Microsoft.Extensions.DependencyInjection;
using NexusAuthorization.Sdk;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class AspNetCoreResourceTests
{
    [Fact]
    public async Task Authorization_filter_context_uses_configured_claim_and_request_values()
    {
        var provider = new RecordingProvider(new(true, "allow", "allowed"));
        using var services = CreateServices(provider, "employee_id");
        using var scope = services.CreateScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var principal = new ClaimsPrincipal(new ClaimsIdentity(
            [new Claim("employee_id", "employee-42"), new Claim("sub", "wrong-subject")], "Test"));
        var http = new DefaultHttpContext();
        http.Request.Method = HttpMethods.Patch;
        http.Request.Path = "/api/orders/42";
        using var abort = new CancellationTokenSource();
        http.RequestAborted = abort.Token;
        var resource = new AuthorizationFilterContext(
            new ActionContext(http, new RouteData(), new ActionDescriptor()),
            Array.Empty<IFilterMetadata>());

        var result = await authorization.AuthorizeAsync(
            principal,
            resource,
            NexusAuthorizationDefaults.PolicyName);

        Assert.True(result.Succeeded);
        Assert.Equal(1, provider.CallCount);
        Assert.Equal(
            ("employee-42", "PATCH", "/api/orders/42", abort.Token),
            provider.LastRequest);
    }

    [Theory]
    [InlineData(false)]
    [InlineData(true)]
    public async Task Unsupported_resource_type_fails_without_calling_provider(bool useNullResource)
    {
        var provider = new RecordingProvider(new(true, "allow", "allowed"));
        using var services = CreateServices(provider, "uid");
        using var scope = services.CreateScope();
        var authorization = scope.ServiceProvider.GetRequiredService<IAuthorizationService>();
        var principal = new ClaimsPrincipal(new ClaimsIdentity(
            [new Claim("uid", "user-42")], "Test"));
        object? resource = useNullResource ? null : "unsupported";

        var result = await authorization.AuthorizeAsync(
            principal,
            resource,
            NexusAuthorizationDefaults.PolicyName);

        Assert.False(result.Succeeded);
        Assert.Equal(0, provider.CallCount);
        Assert.Null(provider.LastRequest);
    }

    private static ServiceProvider CreateServices(RecordingProvider provider, string subjectClaimType)
    {
        var services = new ServiceCollection();
        services.AddNexusAuthorization(options =>
        {
            options.ServiceResource = "orders.api";
            options.SubjectClaimType = subjectClaimType;
        }).UseProvider(provider);
        return services.BuildServiceProvider(new ServiceProviderOptions { ValidateScopes = true });
    }

    private sealed class RecordingProvider(AuthorizationDecision decision) : INexusAuthorizationProvider
    {
        public int CallCount { get; private set; }

        public (string Subject, string Method, string Path, CancellationToken Token)? LastRequest { get; private set; }

        public Task<AuthorizationDecision> AuthorizeAsync(
            string subject,
            string method,
            string path,
            CancellationToken cancellationToken = default)
        {
            CallCount++;
            LastRequest = (subject, method, path, cancellationToken);
            return Task.FromResult(decision);
        }
    }
}
