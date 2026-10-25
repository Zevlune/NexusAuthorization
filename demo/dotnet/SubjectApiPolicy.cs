using System.Text.Json.Serialization;

namespace Demo.Api;

public sealed class SubjectApiPolicy
{
    // 仅用于识别并拒绝旧协议；零值和缺失值按空应用元数据处理。
    [JsonPropertyName("application_key")]
    public string? ApplicationKey { get; init; }

    [JsonPropertyName("application_id")]
    public Guid? ApplicationId { get; init; }

    [JsonPropertyName("subject")]
    public string? Subject { get; init; }

    [JsonPropertyName("domain")]
    public string? Domain { get; init; }

    [JsonPropertyName("service_resource")]
    public string? ServiceResource { get; init; }

    [JsonPropertyName("service_active")]
    public bool ServiceActive { get; init; }

    [JsonPropertyName("apis")]
    public IReadOnlyList<SubjectApiEndpoint?>? APIs { get; init; }

    [JsonPropertyName("catalog")]
    public IReadOnlyList<SubjectApiEndpoint?>? Catalog { get; init; }

    [JsonPropertyName("roles")]
    public IReadOnlyList<string?>? Roles { get; init; }
}

public sealed class SubjectApiEndpoint
{
    [JsonConstructor]
    public SubjectApiEndpoint(Guid id, string method, string pathTemplate, bool enabled)
    {
        Id = id;
        Method = method;
        PathTemplate = pathTemplate;
        Enabled = enabled;
    }

    [JsonPropertyName("id")]
    public Guid Id { get; }

    [JsonPropertyName("method")]
    public string Method { get; }

    [JsonPropertyName("path_template")]
    public string PathTemplate { get; }

    [JsonPropertyName("enabled")]
    public bool Enabled { get; }
}

public sealed record AuthorizationDecision(bool Allowed, string Effect, string Reason);
