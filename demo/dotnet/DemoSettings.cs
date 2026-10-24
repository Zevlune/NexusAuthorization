namespace Demo.Api;

public sealed class DemoSettings
{
    public AuthSettings Auth { get; init; } = new();
    public AuthorizationCenterSettings AuthorizationCenter { get; init; } = new();
    public RedisSettings Redis { get; init; } = new();
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
    public string AppKey { get; init; } = "";
    public string ServiceResource { get; init; } = "";
    public string Domain { get; init; } = "";
}

public sealed class RedisSettings
{
    public string Host { get; init; } = "192.168.31.200";
    public int Port { get; init; } = 8433;
    public string User { get; init; } = "";
    public string Password { get; init; } = "";
    public int Database { get; init; }
    public string KeyPrefix { get; init; } = "nexusauthorization:dev";
    public int TimeoutMilliseconds { get; init; } = 2000;
}
