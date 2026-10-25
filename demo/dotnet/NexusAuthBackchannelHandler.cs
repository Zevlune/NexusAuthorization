using System.Net;
using System.Net.Http.Headers;
using System.Text;
using Microsoft.AspNetCore.WebUtilities;

namespace Demo.Api;

// ASP.NET Core 默认在表单中发送 client_secret；Basic 客户端仅在 token 请求上转换认证方式。
// Discovery 和 JWKS 请求不携带客户端凭据。
public sealed class NexusAuthBackchannelHandler(AuthSettings settings) : DelegatingHandler(new HttpClientHandler())
{
    private readonly Uri tokenEndpoint = new($"{settings.Authority.TrimEnd('/')}/connect/token");

    protected override async Task<HttpResponseMessage> SendAsync(HttpRequestMessage request, CancellationToken ct)
    {
        if (request.Method == HttpMethod.Post && request.RequestUri == tokenEndpoint
            && request.Content is FormUrlEncodedContent)
        {
            var form = QueryHelpers.ParseQuery(await request.Content.ReadAsStringAsync(ct))
                .ToDictionary(pair => pair.Key, pair => pair.Value.ToString());
            NexusAuthClientAuthentication.Apply(request, form, settings);
            request.Content.Dispose();
            request.Content = new FormUrlEncodedContent(form);
        }

        return await base.SendAsync(request, ct);
    }
}

internal static class NexusAuthClientAuthentication
{
    public static void Apply(HttpRequestMessage request, IDictionary<string, string> form, AuthSettings settings)
    {
        switch (settings.TokenEndpointAuthMethod)
        {
            case "client_secret_basic":
                form.Remove("client_id");
                form.Remove("client_secret");
                var credentials = $"{WebUtility.UrlEncode(settings.ClientId)}:{WebUtility.UrlEncode(settings.ClientSecret)}";
                request.Headers.Authorization = new AuthenticationHeaderValue("Basic",
                    Convert.ToBase64String(Encoding.UTF8.GetBytes(credentials)));
                break;
            case "client_secret_post":
                request.Headers.Authorization = null;
                form["client_id"] = settings.ClientId;
                form["client_secret"] = settings.ClientSecret;
                break;
            default:
                throw new InvalidOperationException(
                    "Auth:TokenEndpointAuthMethod 必须为 client_secret_basic 或 client_secret_post。");
        }
    }
}
