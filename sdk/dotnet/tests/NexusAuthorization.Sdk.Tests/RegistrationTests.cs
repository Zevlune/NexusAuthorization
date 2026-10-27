using System.Reflection;
using System.Runtime.CompilerServices;
using Microsoft.AspNetCore.Authorization;
using Microsoft.Extensions.Configuration;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using NexusAuthorization.Sdk.Redis;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class RegistrationTests
{
    [Fact]
    public void Core_binds_common_configuration_and_uses_an_external_provider()
    {
        var configuration = new ConfigurationBuilder().AddInMemoryCollection(new Dictionary<string, string?>
        {
            ["NexusAuthorization:ServiceResource"] = "orders.api",
            ["NexusAuthorization:SubjectClaimType"] = "uid"
        }).Build();
        var services = new ServiceCollection();
        var expected = new CustomProvider();
        var builder = services.AddNexusAuthorization(options =>
            configuration.GetSection(NexusAuthorizationOptions.SectionName).Bind(options));
        builder.UseProvider(expected);
        using var provider = services.BuildServiceProvider(
            new ServiceProviderOptions { ValidateScopes = true });

        Assert.Same(services, builder.Services);
        Assert.Same(expected, provider.GetRequiredService<INexusAuthorizationProvider>());
        Assert.NotNull(provider.GetRequiredService<ILogger<CustomProvider>>());
        var options = provider.GetRequiredService<IOptions<NexusAuthorizationOptions>>().Value;
        Assert.Equal("orders.api", options.ServiceResource);
        Assert.Equal("uid", options.SubjectClaimType);
    }

    [Fact]
    public void Public_authorization_contracts_do_not_expose_domain()
    {
        Assert.Null(typeof(NexusAuthorizationOptions).GetProperty("Domain"));
        Assert.Null(typeof(SubjectApiPolicy).GetProperty("Domain"));
        Assert.Null(typeof(RedisAuthorizationStore).GetMethod("NormalizeDomain"));
    }

    [Fact]
    public void Core_does_not_expose_configuration_overload()
    {
        var configurationOverload = typeof(ServiceCollectionExtensions)
            .GetMethods()
            .SingleOrDefault(method =>
                method.Name == nameof(ServiceCollectionExtensions.AddNexusAuthorization) &&
                method.GetParameters().Any(parameter =>
                    parameter.ParameterType == typeof(IConfiguration)));

        Assert.Null(configurationOverload);
    }

    [Theory]
    [InlineData(
        typeof(RedisBuilderExtensions),
        nameof(RedisBuilderExtensions.UseRedis),
        typeof(RedisAuthorizationOptions))]
    [InlineData(
        typeof(HttpBuilderExtensions),
        nameof(HttpBuilderExtensions.UseHttp),
        typeof(HttpAuthorizationOptions))]
    public void Transport_extensions_expose_single_void_action_overload(
        Type extensionType,
        string methodName,
        Type optionsType)
    {
        var method = Assert.Single(
            extensionType.GetMethods(BindingFlags.Public | BindingFlags.Static),
            candidate => candidate.Name == methodName);

        Assert.Equal(typeof(void), method.ReturnType);
        var parameters = method.GetParameters();
        Assert.Equal(2, parameters.Length);
        Assert.Equal(typeof(NexusAuthorizationBuilder), parameters[0].ParameterType);
        Assert.True(method.IsDefined(typeof(ExtensionAttribute), false));
        Assert.Equal(
            typeof(Action<>).MakeGenericType(optionsType),
            parameters[1].ParameterType);
    }

    [Fact]
    public void Core_has_no_dependency_on_transport_implementations()
    {
        var dependencies = typeof(NexusAuthorizationBuilder).Assembly.GetReferencedAssemblies();
        Assert.DoesNotContain(dependencies, dependency =>
            dependency.Name is "StackExchange.Redis" or "NexusAuthorization.Sdk.Redis" or
                "NexusAuthorization.Sdk.Http" or "Microsoft.Extensions.Http");
        var services = new ServiceCollection();
        services.AddNexusAuthorization();
        Assert.DoesNotContain(services, descriptor =>
            descriptor.ServiceType.Assembly.GetName().Name is "StackExchange.Redis" or
                "NexusAuthorization.Sdk.Redis" or "NexusAuthorization.Sdk.Http");
    }

    [Fact]
    public void Last_generic_provider_selection_wins_across_builders_and_external_registration()
    {
        var services = new ServiceCollection();
        services.AddSingleton<INexusAuthorizationProvider>(new CustomProvider());
        var firstBuilder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
        firstBuilder.UseProvider<CustomProvider>();
        var secondBuilder = services.AddNexusAuthorization();
        secondBuilder.UseProvider<AnotherProvider>();
        using var provider = services.BuildServiceProvider(
            new ServiceProviderOptions { ValidateScopes = true });

        var descriptor = Assert.Single(
            services,
            candidate => candidate.ServiceType == typeof(INexusAuthorizationProvider));
        Assert.Equal(ServiceLifetime.Scoped, descriptor.Lifetime);
        Assert.Equal(typeof(AnotherProvider), descriptor.ImplementationType);
        Assert.Null(descriptor.ImplementationFactory);
        using var scope = provider.CreateScope();
        Assert.IsType<AnotherProvider>(
            scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());
    }

    [Fact]
    public void Generic_provider_receives_scoped_dependencies_through_native_DI()
    {
        var services = new ServiceCollection();
        services.AddRouting();
        services.AddScoped<ScopedDependency>();
        services.AddNexusAuthorization(options => options.ServiceResource = "orders.api")
            .UseProvider<ProviderWithScopedDependency>();
        using var provider = services.BuildServiceProvider(
            new ServiceProviderOptions
            {
                ValidateOnBuild = true,
                ValidateScopes = true
            });
        using var scope = provider.CreateScope();

        var authorization = Assert.IsType<ProviderWithScopedDependency>(
            scope.ServiceProvider.GetRequiredService<INexusAuthorizationProvider>());

        Assert.Same(
            scope.ServiceProvider.GetRequiredService<ScopedDependency>(),
            authorization.Dependency);
    }

    [Fact]
    public void Generic_provider_registration_is_visible_to_ValidateOnBuild()
    {
        var services = new ServiceCollection();
        services.AddRouting();
        services.AddNexusAuthorization(options => options.ServiceResource = "orders.api")
            .UseProvider<ProviderWithScopedDependency>();

        var error = Assert.Throws<AggregateException>(() =>
            services.BuildServiceProvider(
                new ServiceProviderOptions
                {
                    ValidateOnBuild = true,
                    ValidateScopes = true
                }));

        Assert.Contains(nameof(ScopedDependency), error.ToString());
    }

    [Theory]
    [InlineData(true)]
    [InlineData(false)]
    public async Task Cross_transport_reselection_uses_last_provider_and_keeps_both_transport_registrations(
        bool redisFirst)
    {
        ServiceDescriptor[] descriptors = [];
        using var host = new HostBuilder().ConfigureServices(services =>
        {
            var builder = services.AddNexusAuthorization(options => options.ServiceResource = "orders.api");
            if (redisFirst)
            {
                builder.UseRedis(ConfigureValidRedis);
                builder.UseHttp(ConfigureValidHttp);
            }
            else
            {
                builder.UseHttp(ConfigureValidHttp);
                builder.UseRedis(ConfigureValidRedis);
            }

            descriptors = services.ToArray();
        }).Build();

        await host.StartAsync();

        var provider = Assert.Single(
            descriptors,
            descriptor => descriptor.ServiceType == typeof(INexusAuthorizationProvider));
        Assert.Equal(ServiceLifetime.Scoped, provider.Lifetime);
        Assert.Equal(
            redisFirst ? typeof(HttpAuthorizationProvider) : typeof(RedisAuthorizationStore),
            provider.ImplementationType);
        Assert.Contains(
            descriptors,
            descriptor => descriptor.ServiceType == typeof(IHttpClientFactory));
        Assert.Contains(
            descriptors,
            descriptor => descriptor.ServiceType.FullName == "StackExchange.Redis.IConnectionMultiplexer");
        Assert.DoesNotContain(
            descriptors,
            descriptor => descriptor.ServiceType == typeof(IValidateOptions<HttpAuthorizationOptions>));
        Assert.DoesNotContain(
            descriptors,
            descriptor => descriptor.ServiceType == typeof(IValidateOptions<RedisAuthorizationOptions>));

        await host.StopAsync();
    }

    [Fact]
    public async Task Missing_provider_succeeds_at_host_startup_and_fails_when_handler_is_resolved()
    {
        using var host = new HostBuilder().ConfigureServices(services =>
            services.AddNexusAuthorization(options => options.ServiceResource = "orders.api")).Build();

        await host.StartAsync();

        using (var scope = host.Services.CreateScope())
            Assert.Throws<InvalidOperationException>(() =>
                scope.ServiceProvider.GetRequiredService<IAuthorizationHandler>());

        await host.StopAsync();
    }

    [Theory]
    [InlineData("", "sub", "ServiceResource")]
    [InlineData("orders.api", "", "SubjectClaimType")]
    public async Task Invalid_common_configuration_fails_host_startup(
        string resource,
        string claim,
        string expectedOption)
    {
        using var host = new HostBuilder().ConfigureServices(services =>
            services.AddNexusAuthorization(options =>
            {
                options.ServiceResource = resource;
                options.SubjectClaimType = claim;
            }).UseProvider<CustomProvider>()).Build();

        var error = await Assert.ThrowsAsync<OptionsValidationException>(() => host.StartAsync());

        Assert.Contains(expectedOption, error.Message);
    }

    private sealed class CustomProvider : INexusAuthorizationProvider
    {
        public CustomProvider() { }
        public Task<AuthorizationDecision> AuthorizeAsync(string subject, string method, string path,
            CancellationToken cancellationToken = default) => Task.FromResult(new AuthorizationDecision(false, "deny", "test"));
    }

    private sealed class AnotherProvider : INexusAuthorizationProvider
    {
        public Task<AuthorizationDecision> AuthorizeAsync(string subject, string method, string path,
            CancellationToken cancellationToken = default) => Task.FromResult(new AuthorizationDecision(false, "deny", "test"));
    }

    private sealed class ScopedDependency
    {
        public ScopedDependency()
        {
        }
    }

    private sealed class ProviderWithScopedDependency : INexusAuthorizationProvider
    {
        public ProviderWithScopedDependency(ScopedDependency dependency)
        {
            Dependency = dependency;
        }

        public ScopedDependency Dependency { get; }

        public Task<AuthorizationDecision> AuthorizeAsync(string subject, string method, string path,
            CancellationToken cancellationToken = default) =>
            Task.FromResult(new AuthorizationDecision(false, "deny", "test"));
    }

    private static void ConfigureValidHttp(HttpAuthorizationOptions options)
    {
        options.BaseUrl = "https://permissions.example";
        options.AppKey = "orders";
        options.ServiceCredential = "service-token";
    }

    private static void ConfigureValidRedis(RedisAuthorizationOptions options)
    {
        options.Host = "redis.internal";
        options.Port = 6379;
        options.KeyPrefix = "nexus:test";
        options.TimeoutMilliseconds = 2000;
    }
}
