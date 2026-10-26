using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;

namespace NexusAuthorization.Sdk;

/// <summary>实现库通过此 Builder 注册自己的连接、配置和统一授权接口实现。</summary>
public sealed class NexusAuthorizationBuilder
{
    internal NexusAuthorizationBuilder(IServiceCollection services)
    {
        Services = services;
    }

    public IServiceCollection Services { get; }

    /// <summary>注册作用域授权实现；重复调用时使用最后一次选择。</summary>
    public NexusAuthorizationBuilder UseProvider<TProvider>()
        where TProvider : class, INexusAuthorizationProvider
    {
        Services.RemoveAll<INexusAuthorizationProvider>();
        Services.AddScoped<INexusAuthorizationProvider, TProvider>();
        return this;
    }

    /// <summary>使用由调用方拥有的实例；SDK 不负责释放该实例。</summary>
    public NexusAuthorizationBuilder UseProvider<TProvider>(TProvider provider)
        where TProvider : class, INexusAuthorizationProvider
    {
        ArgumentNullException.ThrowIfNull(provider);
        Services.RemoveAll<INexusAuthorizationProvider>();
        Services.AddSingleton<INexusAuthorizationProvider>(provider);
        return this;
    }
}
