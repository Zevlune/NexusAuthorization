using System.Net;
using System.Text.Json.Nodes;
using Microsoft.Extensions.DependencyInjection;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Redis;
using StackExchange.Redis;
using Xunit;

namespace Demo.Api.IntegrationTests;

public sealed class RedisSdkBoundaryTests(RedisFixture fixture) : IClassFixture<RedisFixture>
{
    [Theory]
    [InlineData("")]
    [InlineData("   ")]
    public async Task Invalid_catalog_template_denies_even_an_otherwise_granted_controller_action(string template)
    {
        using var factory = fixture.CreateFactory();
        var policy = TestPolicies.Create(factory);
        policy["catalog"]!.AsArray().Add(new JsonObject
        {
            ["id"] = Guid.NewGuid().ToString(), ["method"] = "GET",
            ["path_template"] = template, ["enabled"] = true
        });
        await fixture.PublishAsync(factory, policy);
        using var client = factory.CreateAuthenticatedClient();

        using var response = await client.GetAsync("/api/demo/orders");

        Assert.Equal(HttpStatusCode.Forbidden, response.StatusCode);
    }

    [Fact]
    public async Task Caller_owned_redis_connection_remains_usable_after_sdk_container_is_disposed()
    {
        using var factory = fixture.CreateFactory();
        var services = new ServiceCollection();
        services.AddSingleton<IConnectionMultiplexer>(fixture.Database.Multiplexer);
        services.AddNexusAuthorization(factory.Settings.ConfigureAuthorization)
            .UseRedis(factory.Settings.ConfigureRedis);
        using (var root = services.BuildServiceProvider(new ServiceProviderOptions { ValidateScopes = true }))
        {
            using var scope = root.CreateScope();
            var result = await scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>()
                .AuthorizeAsync(TestIdentity.Subject, "GET", "/api/demo/orders");
            Assert.False(result.Allowed);
            Assert.Equal("endpoint_not_granted", result.Reason);
        }

        await fixture.Database.PingAsync();
        var key = $"sdk-connection-ownership:{Guid.NewGuid():N}";
        Assert.True(await fixture.Database.StringSetAsync(key, "still-connected"));
        Assert.Equal("still-connected", (string?)await fixture.Database.StringGetAsync(key));
    }

    [Fact]
    public async Task Cancelled_request_does_not_use_a_published_allow_policy()
    {
        using var factory = fixture.CreateFactory();
        await fixture.PublishAsync(factory, TestPolicies.Create(factory));
        using var scope = factory.Services.CreateScope();
        var provider = scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();
        using var cancelled = new CancellationTokenSource();
        cancelled.Cancel();

        var decision = await provider.AuthorizeAsync(TestIdentity.Subject, "GET", "/api/demo/orders", cancelled.Token);

        Assert.Equal(new AuthorizationDecision(false, "deny", "request_cancelled"), decision);
    }
}
