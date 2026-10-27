using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using NexusAuthorization.Sdk.Redis;

namespace Demo.Api;

public sealed class DemoSettings
{
    public AuthSettings Auth { get; init; } = new();
    public AuthorizationCenterSettings AuthorizationCenter { get; init; } = new();
    public RedisAuthorizationOptions Redis { get; init; } = new();
    public HttpAuthorizationOptions Http { get; init; } = new();

    // 保留 Demo 现有配置节及 user-secrets 路径；其他项目可直接绑定 SDK 配置类。
    public void ConfigureAuthorization(NexusAuthorizationOptions options)
    {
        options.ServiceResource = AuthorizationCenter.ServiceResource;
        options.SubjectClaimType = AuthorizationCenter.SubjectClaimType;
    }

    public void ConfigureRedis(RedisAuthorizationOptions options)
    {
        options.Host = Redis.Host;
        options.Port = Redis.Port;
        options.User = Redis.User;
        options.Password = Redis.Password;
        options.Database = Redis.Database;
        options.KeyPrefix = Redis.KeyPrefix;
        options.TimeoutMilliseconds = Redis.TimeoutMilliseconds;
        options.Ssl = Redis.Ssl;
        options.SslHost = Redis.SslHost;
    }

    public void ConfigureHttp(HttpAuthorizationOptions options)
    {
        options.BaseUrl = Http.BaseUrl;
        options.AppKey = AuthorizationCenter.AppKey;
        options.ServiceCredential = Http.ServiceCredential;
        options.TimeoutMilliseconds = Http.TimeoutMilliseconds;
    }
}

public sealed class AuthSettings
{
    public string Authority { get; init; } = "";
    public string ClientId { get; init; } = "";
    public string TokenEndpointAuthMethod { get; init; } = "client_secret_basic";
    public string ClientSecret { get; init; } = "";
    public string Audience { get; init; } = "";
    public string ApiScope { get; init; } = "";
    public bool RequireHttpsMetadata { get; init; } = true;
    public string RedirectUri { get; init; } = "";
    public string PostLogoutRedirectUri { get; init; } = "";
}

public sealed class AuthorizationCenterSettings
{
    public string Provider { get; init; } = "Redis";
    public string SubjectClaimType { get; init; } = "sub";
    public string AppKey { get; init; } = "";
    public string ServiceResource { get; init; } = "";
}
