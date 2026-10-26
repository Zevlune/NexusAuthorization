namespace NexusAuthorization.Sdk.Http;

public sealed class HttpAuthorizationOptions
{
    public string BaseUrl { get; set; } = "";
    public string AppKey { get; set; } = "";
    /// <summary>可选的固定 Bearer 凭证；未设置时转发当前 HTTP 请求的 Bearer Token。</summary>
    public string ServiceCredential { get; set; } = "";
    public int TimeoutMilliseconds { get; set; } = 3000;
}
