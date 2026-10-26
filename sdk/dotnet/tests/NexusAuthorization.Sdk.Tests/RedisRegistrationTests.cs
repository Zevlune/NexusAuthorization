using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Http;
using Microsoft.Extensions.Options;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using NexusAuthorization.Sdk.Redis;
using StackExchange.Redis;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class RedisRegistrationTests
{
    [Theory]
    [InlineData("", 6379, 0, "nexus:test", 2000)]
    [InlineData("localhost", 0, 0, "nexus:test", 2000)]
    [InlineData("localhost", 65536, 0, "nexus:test", 2000)]
    [InlineData("localhost", 6379, -1, "nexus:test", 2000)]
    [InlineData("localhost", 6379, 0, "nexus:test:", 2000)]
    [InlineData("localhost", 6379, 0, "nexus:{test}", 2000)]
    [InlineData("localhost", 6379, 0, "nexus: test", 2000)]
    [InlineData("localhost", 6379, 0, "nexus:test", 0)]
    public void Invalid_redis_configuration_passes_through_without_connecting(
        string host,
        int port,
        int database,
        string keyPrefix,
        int timeoutMilliseconds)
    {
        var connectionFactoryCalls = 0;
        var services = new ServiceCollection();
        services.AddSingleton<IConnectionMultiplexer>(_ =>
        {
            connectionFactoryCalls++;
            throw new InvalidOperationException("The test must not create a Redis connection.");
        });
        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseRedis(options =>
        {
            options.Host = host;
            options.Port = port;
            options.Database = database;
            options.KeyPrefix = keyPrefix;
            options.TimeoutMilliseconds = timeoutMilliseconds;
        });
        using var provider = services.BuildServiceProvider();

        var redis = provider.GetRequiredService<IOptions<RedisAuthorizationOptions>>().Value;

        Assert.Equal(host, redis.Host);
        Assert.Equal(port, redis.Port);
        Assert.Equal(database, redis.Database);
        Assert.Equal(keyPrefix, redis.KeyPrefix);
        Assert.Equal(timeoutMilliseconds, redis.TimeoutMilliseconds);
        Assert.Equal(0, connectionFactoryCalls);
    }

    [Fact]
    public void Common_and_redis_configuration_bind_independently()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["NexusAuthorization:ServiceResource"] = "orders.api",
                ["NexusAuthorization:Domain"] = "tenant-a",
                ["NexusAuthorization:Redis:Host"] = "redis.internal",
                ["NexusAuthorization:Redis:Port"] = "6380",
                ["NexusAuthorization:Redis:Database"] = "3"
            })
            .Build();
        var services = new ServiceCollection();

        var builder = services.AddNexusAuthorization(
            options => configuration
                .GetSection(NexusAuthorizationOptions.SectionName)
                .Bind(options));
        builder.UseRedis(options => configuration
            .GetSection("NexusAuthorization:Redis")
            .Bind(options));

        using var provider = services.BuildServiceProvider();
        var common = provider.GetRequiredService<IOptions<NexusAuthorizationOptions>>().Value;
        var redis = provider.GetRequiredService<IOptions<RedisAuthorizationOptions>>().Value;

        Assert.Equal("orders.api", common.ServiceResource);
        Assert.Equal("tenant-a", common.Domain);
        Assert.Equal("redis.internal", redis.Host);
        Assert.Equal(6380, redis.Port);
        Assert.Equal(3, redis.Database);
    }

    [Fact]
    public void Explicit_redis_configuration_applies_in_registration_order()
    {
        var services = new ServiceCollection();

        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseRedis(options => options.Host = "first.redis");
        builder.UseRedis(options => options.Host = "second.redis");

        using var provider = services.BuildServiceProvider();
        var redis = provider.GetRequiredService<IOptions<RedisAuthorizationOptions>>().Value;

        Assert.Equal("second.redis", redis.Host);
    }

    [Fact]
    public void Use_redis_does_not_register_http_or_remote_services()
    {
        var services = new ServiceCollection();

        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseRedis(_ => { });

        Assert.DoesNotContain(
            services,
            descriptor => descriptor.ServiceType == typeof(IHttpClientFactory) ||
                          descriptor.ServiceType == typeof(HttpAuthorizationProvider));
    }

    [Fact]
    public void Redis_connection_is_registered_as_a_deferred_singleton()
    {
        var services = new ServiceCollection();

        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseRedis(_ => { });

        var descriptor = Assert.Single(
            services,
            candidate => candidate.ServiceType == typeof(IConnectionMultiplexer));
        Assert.Equal(ServiceLifetime.Singleton, descriptor.Lifetime);
        Assert.NotNull(descriptor.ImplementationFactory);

        using var provider = services.BuildServiceProvider();
        _ = provider.GetRequiredService<IOptions<RedisAuthorizationOptions>>().Value;
    }

    [Fact]
    public void Existing_redis_connection_registration_is_preserved()
    {
        var services = new ServiceCollection();
        var existingFactory = new Func<IServiceProvider, IConnectionMultiplexer>(
            _ => throw new InvalidOperationException("The test must not create a Redis connection."));
        services.AddSingleton(existingFactory);

        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseRedis(_ => { });

        var descriptor = Assert.Single(
            services,
            candidate => candidate.ServiceType == typeof(IConnectionMultiplexer));
        Assert.Same(existingFactory, descriptor.ImplementationFactory);
        Assert.Equal(ServiceLifetime.Singleton, descriptor.Lifetime);
    }

    [Fact]
    public void Repeated_use_redis_keeps_provider_and_connection_registration_idempotent()
    {
        var services = new ServiceCollection();

        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseRedis(_ => { });
        builder.UseRedis(_ => { });

        Assert.DoesNotContain(
            services,
            descriptor => descriptor.ServiceType == typeof(RedisAuthorizationStore));
        Assert.Single(
            services,
            descriptor => descriptor.ServiceType == typeof(INexusAuthorizationProvider));
        Assert.Single(
            services,
            descriptor => descriptor.ServiceType == typeof(IConnectionMultiplexer));
    }
}
