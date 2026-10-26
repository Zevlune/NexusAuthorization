using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Options;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using StackExchange.Redis;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class HttpRegistrationTests
{
    [Fact]
    public void Configuration_section_binds_http_options()
    {
        var configuration = new ConfigurationBuilder()
            .AddInMemoryCollection(new Dictionary<string, string?>
            {
                ["Http:BaseUrl"] = "https://permissions.example",
                ["Http:AppKey"] = "orders",
                ["Http:ServiceCredential"] = "service-token",
                ["Http:TimeoutMilliseconds"] = "4500"
            })
            .Build();
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(options => configuration.GetSection("Http").Bind(options));
        using var provider = services.BuildServiceProvider();

        var options = provider
            .GetRequiredService<IOptions<HttpAuthorizationOptions>>()
            .Value;

        Assert.Equal("https://permissions.example", options.BaseUrl);
        Assert.Equal("orders", options.AppKey);
        Assert.Equal("service-token", options.ServiceCredential);
        Assert.Equal(4500, options.TimeoutMilliseconds);
    }

    [Theory]
    [InlineData("")]
    [InlineData(null)]
    public void Empty_or_null_service_credential_is_valid_for_default_forwarding(
        string? serviceCredential)
    {
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(options =>
        {
            options.BaseUrl = "https://permissions.example";
            options.AppKey = "orders";
            options.ServiceCredential = serviceCredential!;
        });
        using var provider = services.BuildServiceProvider();

        var options = provider
            .GetRequiredService<IOptions<HttpAuthorizationOptions>>()
            .Value;

        Assert.Equal(serviceCredential, options.ServiceCredential);
    }

    [Theory]
    [InlineData("ftp://permissions.example", "orders", "service-token", 3000)]
    [InlineData("https://user:password@permissions.example", "orders", "service-token", 3000)]
    [InlineData("https://permissions.example?token=secret", "orders", "service-token", 3000)]
    [InlineData("https://permissions.example/#fragment", "orders", "service-token", 3000)]
    [InlineData("https://permissions.example", "", "service-token", 3000)]
    [InlineData("https://permissions.example", "orders", "   ", 3000)]
    [InlineData("https://permissions.example", "orders", "invalid\r\ntoken", 3000)]
    [InlineData("https://permissions.example", "orders", "service-token", 0)]
    [InlineData("https://permissions.example", "orders", "service-token", 60001)]
    public void Invalid_http_configuration_is_preserved_without_outbound_call(
        string baseUrl,
        string appKey,
        string serviceCredential,
        int timeoutMilliseconds)
    {
        var requestCount = 0;
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(options =>
        {
            options.BaseUrl = baseUrl;
            options.AppKey = appKey;
            options.ServiceCredential = serviceCredential;
            options.TimeoutMilliseconds = timeoutMilliseconds;
        });
        services.AddHttpClient(HttpAuthorizationProvider.HttpClientName)
            .ConfigurePrimaryHttpMessageHandler(() =>
                new CountingHttpMessageHandler(() => requestCount++));
        using var provider = services.BuildServiceProvider();

        var options = provider
            .GetRequiredService<IOptions<HttpAuthorizationOptions>>()
            .Value;
        using var scope = provider.CreateScope();

        Assert.IsType<HttpAuthorizationProvider>(
            scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        Assert.Equal(baseUrl, options.BaseUrl);
        Assert.Equal(appKey, options.AppKey);
        Assert.Equal(serviceCredential, options.ServiceCredential);
        Assert.Equal(timeoutMilliseconds, options.TimeoutMilliseconds);
        Assert.Equal(0, requestCount);
    }

    [Fact]
    public void UseHttp_does_not_register_redis()
    {
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(CreateValidHttpOptions);

        Assert.DoesNotContain(
            services,
            descriptor => descriptor.ServiceType == typeof(IConnectionMultiplexer));
    }

    [Fact]
    public void Bare_service_collection_resolves_real_provider_without_network_access()
    {
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(CreateValidHttpOptions);
        using var provider = services.BuildServiceProvider(
            new ServiceProviderOptions { ValidateScopes = true });
        using var scope = provider.CreateScope();

        var authorization =
            scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>();

        Assert.IsType<HttpAuthorizationProvider>(authorization);
    }

    [Fact]
    public void UseHttp_registers_http_context_accessor_by_default()
    {
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(CreateValidHttpOptions);
        using var provider = services.BuildServiceProvider();

        Assert.Single(
            services,
            descriptor => descriptor.ServiceType == typeof(IHttpContextAccessor));
        Assert.IsType<HttpContextAccessor>(
            provider.GetRequiredService<IHttpContextAccessor>());
    }

    [Fact]
    public void UseHttp_preserves_preexisting_http_context_accessor()
    {
        var existingAccessor = new HttpContextAccessor();
        var services = new ServiceCollection();
        services.AddSingleton<IHttpContextAccessor>(existingAccessor);

        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(CreateValidHttpOptions);
        using var provider = services.BuildServiceProvider();

        var descriptor = Assert.Single(
            services,
            candidate => candidate.ServiceType == typeof(IHttpContextAccessor));
        Assert.Same(existingAccessor, descriptor.ImplementationInstance);
        Assert.Same(
            existingAccessor,
            provider.GetRequiredService<IHttpContextAccessor>());
    }

    [Fact]
    public void UseHttp_resolves_provider_with_default_empty_credential()
    {
        var services = new ServiceCollection();
        var builder = services.AddNexusAuthorization(options =>
            options.ServiceResource = "orders.api");
        builder.UseHttp(options =>
        {
            options.BaseUrl = "https://permissions.example";
            options.AppKey = "orders";
        });
        using var provider = services.BuildServiceProvider(
            new ServiceProviderOptions { ValidateScopes = true });
        using var scope = provider.CreateScope();

        Assert.IsType<HttpAuthorizationProvider>(
            scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
        Assert.IsType<HttpContextAccessor>(
            scope.ServiceProvider.GetRequiredService<IHttpContextAccessor>());
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
            throw new InvalidOperationException("Invalid HTTP options must not issue a request.");
        }
    }
}
