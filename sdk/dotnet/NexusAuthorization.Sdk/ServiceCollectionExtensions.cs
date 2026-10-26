using Microsoft.AspNetCore.Authorization;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;

namespace NexusAuthorization.Sdk;

public static class ServiceCollectionExtensions
{
    public static NexusAuthorizationBuilder AddNexusAuthorization(this IServiceCollection services)
    {
        ArgumentNullException.ThrowIfNull(services);
        // 独立 ServiceCollection 也可使用 SDK；不添加或替换宿主的日志输出 Provider。
        services.AddLogging();
        services.AddOptions<NexusAuthorizationOptions>().ValidateOnStart();
        services.TryAddEnumerable(ServiceDescriptor.Singleton<
            IValidateOptions<NexusAuthorizationOptions>, NexusAuthorizationOptionsValidator>());
        services.AddAuthorization(options => options.AddPolicy(NexusAuthorizationDefaults.PolicyName,
            policy => policy.RequireAuthenticatedUser().AddRequirements(new NexusAuthorizationRequirement())));
        services.TryAddEnumerable(ServiceDescriptor.Scoped<IAuthorizationHandler, NexusAuthorizationHandler>());

        return new NexusAuthorizationBuilder(services);
    }

    public static NexusAuthorizationBuilder AddNexusAuthorization(this IServiceCollection services,
        Action<NexusAuthorizationOptions> configure)
    {
        ArgumentNullException.ThrowIfNull(configure);
        var builder = services.AddNexusAuthorization();
        services.Configure(configure);
        return builder;
    }
}
