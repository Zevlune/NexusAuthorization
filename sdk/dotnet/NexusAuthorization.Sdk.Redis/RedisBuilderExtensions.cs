using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Options;
using StackExchange.Redis;

namespace NexusAuthorization.Sdk.Redis;

public static class RedisBuilderExtensions
{
    public static void UseRedis(
        this NexusAuthorizationBuilder builder,
        Action<RedisAuthorizationOptions> configure)
    {
        ArgumentNullException.ThrowIfNull(builder);
        ArgumentNullException.ThrowIfNull(configure);

        builder.UseProvider<RedisAuthorizationStore>();
        builder.Services.AddOptions<RedisAuthorizationOptions>()
            .Configure(configure);

        // 延迟创建共享连接；已有外部连接由 TryAddSingleton 保留。
        builder.Services.TryAddSingleton<IConnectionMultiplexer>(provider =>
        {
            var settings = provider
                .GetRequiredService<IOptions<RedisAuthorizationOptions>>()
                .Value;
            var connectionOptions = new ConfigurationOptions
            {
                AbortOnConnectFail = false,
                ConnectRetry = 0,
                ConnectTimeout = settings.TimeoutMilliseconds,
                AsyncTimeout = settings.TimeoutMilliseconds,
                SyncTimeout = settings.TimeoutMilliseconds,
                DefaultDatabase = settings.Database,
                User = string.IsNullOrEmpty(settings.User) ? null : settings.User,
                Password = settings.Password,
                Ssl = settings.Ssl,
                SslHost = settings.SslHost,
                ClientName = "NexusAuthorization.Sdk",
                BacklogPolicy = BacklogPolicy.FailFast
            };
            connectionOptions.EndPoints.Add(settings.Host, settings.Port);
            return ConnectionMultiplexer.Connect(connectionOptions);
        });
    }
}
