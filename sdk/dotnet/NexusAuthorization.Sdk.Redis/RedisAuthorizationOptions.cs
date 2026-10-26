namespace NexusAuthorization.Sdk.Redis;

public sealed class RedisAuthorizationOptions
{
    public string Host { get; set; } = "localhost";
    public int Port { get; set; } = 6379;
    public string User { get; set; } = "";
    public string Password { get; set; } = "";
    public int Database { get; set; }
    public string KeyPrefix { get; set; } = "nexusauthorization:dev";
    public int TimeoutMilliseconds { get; set; } = 2000;
    public bool Ssl { get; set; }
    public string? SslHost { get; set; }
}
