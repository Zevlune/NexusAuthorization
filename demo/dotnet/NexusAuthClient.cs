using System.Net;
using System.Text.Json;

namespace Demo.Api;

// 通过登录中心检查撤权状态，不引用 NexusAuth 的数据库或项目。
public sealed class NexusAuthClient(HttpClient http, DemoSettings settings, ILogger<NexusAuthClient> logger)
{
    public async Task<bool> IsActiveAsync(string? token, string? subject, CancellationToken ct)
    {
        var auth = settings.Auth;
        if (string.IsNullOrWhiteSpace(token) || string.IsNullOrWhiteSpace(subject)
            || string.IsNullOrWhiteSpace(auth.ClientSecret))
            return false;

        try
        {
            var form = new Dictionary<string, string>
            {
                ["token"] = token,
                ["token_type_hint"] = "access_token"
            };
            using var request = new HttpRequestMessage(HttpMethod.Post,
                $"{auth.Authority.TrimEnd('/')}/connect/introspect");
            NexusAuthClientAuthentication.Apply(request, form, auth);
            request.Content = new FormUrlEncodedContent(form);
            using var response = await http.SendAsync(request, ct);
            if (response.StatusCode != HttpStatusCode.OK)
                return false;

            using var document = await response.Content.ReadFromJsonAsync<JsonDocument>(ct);
            if (document is null)
                return false;

            var result = document.RootElement;
            return result.GetProperty("active").GetBoolean()
                && result.GetProperty("sub").GetString() == subject
                && result.GetProperty("client_id").GetString() == auth.ClientId
                && result.GetProperty("iss").GetString() == auth.Authority
                && result.GetProperty("token_use").GetString() == "access_token"
                && result.GetProperty("exp").GetInt64() > DateTimeOffset.UtcNow.ToUnixTimeSeconds()
                && (result.GetProperty("scope").GetString() ?? "")
                    .Split(' ', StringSplitOptions.RemoveEmptyEntries).Contains(auth.ApiScope);
        }
        catch (Exception exception) when (exception is HttpRequestException or OperationCanceledException
            or JsonException or InvalidOperationException or KeyNotFoundException or FormatException)
        {
            logger.LogWarning("登录中心令牌检查失败：{FailureType}", exception.GetType().Name);
            return false;
        }
    }
}
