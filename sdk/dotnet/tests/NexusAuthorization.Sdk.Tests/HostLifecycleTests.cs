using System.Net.Http;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Options;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using NexusAuthorization.Sdk.Redis;
using StackExchange.Redis;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class HostLifecycleTests
{
    [Fact]
    public async Task Selected_invalid_redis_configuration_starts_without_connecting_or_validating()
    {
        var connectionFactoryCalls = 0;
        ServiceDescriptor[] descriptors = [];
        using var host = new HostBuilder()
            .ConfigureServices(services =>
            {
                services.AddSingleton<IConnectionMultiplexer>(_ =>
                {
                    connectionFactoryCalls++;
                    throw new InvalidOperationException("Host startup must not create a Redis connection.");
                });
                var builder =
                    services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
                builder.UseRedis(options =>
                {
                    options.Host = "";
                    options.TimeoutMilliseconds = 0;
                });
                descriptors = services.ToArray();
            })
            .Build();

        await host.StartAsync();

        Assert.Equal(0, connectionFactoryCalls);
        Assert.DoesNotContain(
            descriptors,
            descriptor => descriptor.ServiceType == typeof(IValidateOptions<RedisAuthorizationOptions>));
        await host.StopAsync();
    }

    [Fact]
    public async Task Selected_invalid_http_configuration_starts_without_request_or_validator()
    {
        var requestCount = 0;
        ServiceDescriptor[] descriptors = [];
        using var host = new HostBuilder()
            .ConfigureServices(services =>
            {
                var builder =
                    services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
                builder.UseHttp(options =>
                {
                    options.BaseUrl = "ftp://permissions.example";
                    options.AppKey = "orders";
                    options.ServiceCredential = "service-token";
                });
                services.AddHttpClient(HttpAuthorizationProvider.HttpClientName)
                    .ConfigurePrimaryHttpMessageHandler(() =>
                        new CountingHttpMessageHandler(() => requestCount++));
                descriptors = services.ToArray();
            })
            .Build();

        await host.StartAsync();

        using (var scope = host.Services.CreateScope())
        {
            Assert.IsType<HttpAuthorizationProvider>(
                scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        }
        Assert.Equal(0, requestCount);
        Assert.DoesNotContain(
            descriptors,
            descriptor => descriptor.ServiceType == typeof(IValidateOptions<HttpAuthorizationOptions>));
        await host.StopAsync();
    }

    [Fact]
    public async Task Valid_http_configuration_starts_without_issuing_a_request()
    {
        var requestCount = 0;
        using var host = new HostBuilder()
            .ConfigureServices(services =>
            {
                var builder =
                    services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
                builder.UseHttp(CreateValidHttpOptions);
                services.AddHttpClient(HttpAuthorizationProvider.HttpClientName)
                    .ConfigurePrimaryHttpMessageHandler(() =>
                        new CountingHttpMessageHandler(() => requestCount++));
            })
            .Build();

        await host.StartAsync();

        using (var scope = host.Services.CreateScope())
        {
            Assert.IsType<HttpAuthorizationProvider>(
                scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        }
        await host.StopAsync();
        Assert.Equal(0, requestCount);
    }

    [Fact]
    public async Task Unselected_invalid_redis_section_is_not_validated_or_registered()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["NexusAuthorization:ServiceResource"] = "orders.api",
                ["NexusAuthorization:Redis:Host"] = "",
                ["NexusAuthorization:Redis:Port"] = "0",
                ["NexusAuthorization:Redis:KeyPrefix"] = "invalid prefix:"
            })
            .Build();
        using var host = new HostBuilder()
            .ConfigureAppConfiguration(builder => builder.AddConfiguration(configuration))
            .ConfigureServices((context, services) =>
            {
                var builder = services.AddNexusAuthorization(
                    options => context.Configuration
                        .GetSection(NexusAuthorizationOptions.SectionName)
                        .Bind(options));
                builder.UseHttp(CreateValidHttpOptions);
            })
            .Build();

        await host.StartAsync();

        using (var scope = host.Services.CreateScope())
        {
            Assert.IsType<HttpAuthorizationProvider>(
                scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        }
        Assert.Null(host.Services.GetService<IConnectionMultiplexer>());
        Assert.Null(host.Services.GetService<RedisAuthorizationStore>());
        await host.StopAsync();
    }

    [Fact]
    public void Generic_provider_is_disposed_once_at_scope_end_and_not_again_by_root()
    {
        var services = new ServiceCollection();
        services.AddNexusAuthorization(options => options.ServiceResource = "orders.api")
            .UseProvider<DisposableProvider>();
        var root = services.BuildServiceProvider(
            new ServiceProviderOptions { ValidateScopes = true });
        DisposableProvider first;
        DisposableProvider second;
        try
        {
            using (var scope = root.CreateScope())
                first = Assert.IsType<DisposableProvider>(
                    scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
            using (var scope = root.CreateScope())
                second = Assert.IsType<DisposableProvider>(
                    scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());

            Assert.NotSame(first, second);
            Assert.Equal(1, first.DisposeCount);
            Assert.Equal(1, second.DisposeCount);
        }
        finally
        {
            root.Dispose();
        }

        Assert.Equal(1, first.DisposeCount);
        Assert.Equal(1, second.DisposeCount);
    }

    [Fact]
    public async Task Async_disposable_provider_is_disposed_once_at_scope_end_and_not_again_by_root()
    {
        var services = new ServiceCollection();
        services.AddNexusAuthorization(options => options.ServiceResource = "orders.api")
            .UseProvider<AsyncDisposableProvider>();
        var root = services.BuildServiceProvider(
            new ServiceProviderOptions { ValidateScopes = true });
        AsyncDisposableProvider first;
        AsyncDisposableProvider second;
        await using (var firstScope = root.CreateAsyncScope())
        {
            first = Assert.IsType<AsyncDisposableProvider>(
                firstScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        }
        await using (var secondScope = root.CreateAsyncScope())
            second = Assert.IsType<AsyncDisposableProvider>(
                secondScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());

        Assert.NotSame(first, second);
        Assert.Equal(1, first.DisposeCount);
        Assert.Equal(1, second.DisposeCount);

        await root.DisposeAsync();

        Assert.Equal(1, first.DisposeCount);
        Assert.Equal(1, second.DisposeCount);
    }

    [Fact]
    public async Task Instance_selection_keeps_last_provider_and_never_disposes_caller_owned_instances()
    {
        using var implementation = new DisposableProvider();
        using var sameTypeReplacement = new DisposableProvider();
        var crossTypeReplacement = new AsyncDisposableProvider();
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseProvider(implementation).UseProvider(sameTypeReplacement);

        var descriptor = Assert.Single(
            services,
            candidate => candidate.ServiceType == typeof(INexusAuthorizationProvider));
        Assert.Equal(ServiceLifetime.Singleton, descriptor.Lifetime);
        Assert.Same(sameTypeReplacement, descriptor.ImplementationInstance);

        builder.UseProvider(crossTypeReplacement);

        descriptor = Assert.Single(
            services,
            candidate => candidate.ServiceType == typeof(INexusAuthorizationProvider));
        Assert.Equal(ServiceLifetime.Singleton, descriptor.Lifetime);
        Assert.Same(crossTypeReplacement, descriptor.ImplementationInstance);

        using (var root = services.BuildServiceProvider(
                   new ServiceProviderOptions { ValidateScopes = true }))
        {
            Assert.Same(crossTypeReplacement, root.GetRequiredService<INexusAuthorizationProvider>());
            using var scope = root.CreateScope();
            Assert.Same(
                crossTypeReplacement,
                scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        }

        Assert.Equal(0, implementation.DisposeCount);
        Assert.Equal(0, sameTypeReplacement.DisposeCount);
        Assert.Equal(0, crossTypeReplacement.DisposeCount);

        await crossTypeReplacement.DisposeAsync();

        Assert.Equal(1, crossTypeReplacement.DisposeCount);
    }

    [Fact]
    public void Repeated_use_http_composes_configuration_and_keeps_one_scoped_provider()
    {
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        builder.UseHttp(options =>
        {
            options.BaseUrl = "https://first.example";
            options.AppKey = "orders";
        });
        builder.UseHttp(options =>
        {
            options.BaseUrl = "https://second.example";
            options.ServiceCredential = "service-token";
            options.TimeoutMilliseconds = 4500;
        });
        using var root = services.BuildServiceProvider(
            new ServiceProviderOptions { ValidateScopes = true });

        var options = root.GetRequiredService<IOptions<HttpAuthorizationOptions>>().Value;
        Assert.Equal("https://second.example", options.BaseUrl);
        Assert.Equal("orders", options.AppKey);
        Assert.Equal("service-token", options.ServiceCredential);
        Assert.Equal(4500, options.TimeoutMilliseconds);
        var descriptor = Assert.Single(
            services,
            descriptor => descriptor.ServiceType == typeof(INexusAuthorizationProvider));
        Assert.Equal(ServiceLifetime.Scoped, descriptor.Lifetime);
        Assert.Equal(typeof(HttpAuthorizationProvider), descriptor.ImplementationType);
        Assert.Null(descriptor.ImplementationFactory);

        using var firstScope = root.CreateScope();
        using var secondScope = root.CreateScope();
        var first = firstScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();
        var second = secondScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();

        Assert.Same(first, firstScope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        Assert.NotSame(first, second);
        Assert.Throws<InvalidOperationException>(() =>
            root.GetRequiredService<INexusAuthorizationProvider>());
    }

    private static void CreateValidHttpOptions(HttpAuthorizationOptions options)
    {
        options.BaseUrl = "https://permissions.example";
        options.AppKey = "orders";
        options.ServiceCredential = "service-token";
    }

    private sealed class CountingHttpMessageHandler(Action onSend) : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken)
        {
            onSend();
            throw new InvalidOperationException("Host startup must not issue an authorization request.");
        }
    }

    private sealed class DisposableProvider : INexusAuthorizationProvider, IDisposable
    {
        public DisposableProvider()
        {
        }

        public int DisposeCount { get; private set; }

        public Task<AuthorizationDecision> AuthorizeAsync(
            string subject,
            string method,
            string path,
            CancellationToken cancellationToken = default) =>
            Task.FromResult(new AuthorizationDecision(true, "allow", "allowed"));

        public void Dispose()
        {
            DisposeCount++;
        }
    }

    private sealed class AsyncDisposableProvider : INexusAuthorizationProvider, IAsyncDisposable
    {
        public AsyncDisposableProvider()
        {
        }

        public int DisposeCount { get; private set; }

        public Task<AuthorizationDecision> AuthorizeAsync(string subject, string method, string path,
            CancellationToken cancellationToken = default) => Task.FromResult(new AuthorizationDecision(false, "deny", "test"));

        public ValueTask DisposeAsync()
        {
            DisposeCount++;
            return ValueTask.CompletedTask;
        }
    }
}
