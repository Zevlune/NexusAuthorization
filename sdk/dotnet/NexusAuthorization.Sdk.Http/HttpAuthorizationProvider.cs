using System.Buffers;
using System.Net;
using System.Net.Http.Headers;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using NexusAuthorization.Sdk;

namespace NexusAuthorization.Sdk.Http;

public sealed class HttpAuthorizationProvider(
    IHttpClientFactory clients,
    IOptions<NexusAuthorizationOptions> options,
    IOptions<HttpAuthorizationOptions> httpOptions,
    ILogger<HttpAuthorizationProvider> logger,
    IHttpContextAccessor httpContextAccessor)
    : INexusAuthorizationProvider
{
    public const string HttpClientName = "NexusAuthorization.Http";

    private const int MaxResponseBytes = 1024 * 1024;
    private const string FallbackReason = "remote_unavailable";

    private static readonly JsonSerializerOptions RequestJsonOptions =
        new(JsonSerializerDefaults.Web);

    private readonly IHttpClientFactory _clients = clients ?? throw new ArgumentNullException(nameof(clients));

    private readonly IOptions<NexusAuthorizationOptions> _options =
        options ?? throw new ArgumentNullException(nameof(options));

    private readonly IOptions<HttpAuthorizationOptions> _httpOptions =
        httpOptions ?? throw new ArgumentNullException(nameof(httpOptions));

    private readonly ILogger<HttpAuthorizationProvider> _logger =
        logger ?? throw new ArgumentNullException(nameof(logger));

    private readonly IHttpContextAccessor _httpContextAccessor =
        httpContextAccessor ?? throw new ArgumentNullException(nameof(httpContextAccessor));

    public async Task<AuthorizationDecision> AuthorizeAsync(string subject, string method, string path,
        CancellationToken cancellationToken = default)
    {
        if (cancellationToken.IsCancellationRequested)
        {
            return Reject("request_cancelled");
        }

        var normalizedSubject = subject?.Trim() ?? string.Empty;
        if (normalizedSubject.Length == 0)
        {
            return Reject("subject_required");
        }

        if (string.IsNullOrWhiteSpace(method) ||
            string.IsNullOrWhiteSpace(path))
        {
            return Reject("invalid_authorization_request");
        }

        var options = _options.Value;
        var http = _httpOptions.Value;
        var bearerToken = ResolveBearerToken(http);
        if (bearerToken is null)
        {
            return Reject("bearer_token_required");
        }

        var timeoutMilliseconds = http.TimeoutMilliseconds > 0
            ? http.TimeoutMilliseconds
            : 3000;

        using var timeoutSource =
            CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
        timeoutSource.CancelAfter(TimeSpan.FromMilliseconds(timeoutMilliseconds));

        try
        {
            using var client = _clients.CreateClient(HttpClientName);

            var baseUrl = http.BaseUrl?.Trim().TrimEnd('/') ?? string.Empty;
            using var request = new HttpRequestMessage(HttpMethod.Post, $"{baseUrl}/api/v1/authorize");

            var domain = options.Domain?.Trim() ?? string.Empty;
            if (domain == "global")
            {
                domain = string.Empty;
            }

            var payload = new AuthorizationRequest(
                http.AppKey?.Trim() ?? string.Empty,
                options.ServiceResource?.Trim() ?? string.Empty,
                normalizedSubject,
                domain,
                method,
                path);

            var json = JsonSerializer.Serialize(payload, RequestJsonOptions);
            request.Content = new StringContent(
                json,
                Encoding.UTF8,
                "application/json");
            request.Headers.Authorization = new AuthenticationHeaderValue(
                "Bearer",
                bearerToken);

            using var response = await client.SendAsync(
                request,
                HttpCompletionOption.ResponseHeadersRead,
                timeoutSource.Token);

            if (response.StatusCode != HttpStatusCode.OK)
            {
                return Reject(FallbackReason);
            }

            if (response.Content.Headers.ContentLength > MaxResponseBytes)
            {
                return Reject("invalid_remote_response");
            }

            await using var responseStream =
                await response.Content.ReadAsStreamAsync(timeoutSource.Token);
            var responseBytes = await ReadResponseAsync(
                responseStream,
                timeoutSource.Token);
            if (responseBytes is null)
            {
                return Reject("invalid_remote_response");
            }

            return ParseDecision(responseBytes);
        }
        catch (OperationCanceledException)
        {
            return Reject(cancellationToken.IsCancellationRequested
                ? "request_cancelled"
                : "remote_timeout");
        }
        catch (HttpRequestException)
        {
            return Reject(FallbackReason);
        }
        catch (ArgumentException)
        {
            return Reject(FallbackReason);
        }
        catch (FormatException)
        {
            return Reject(FallbackReason);
        }
        catch (IOException)
        {
            return Reject("invalid_remote_response");
        }
        catch (InvalidOperationException)
        {
            return Reject(FallbackReason);
        }
    }

    private string? ResolveBearerToken(HttpAuthorizationOptions options)
    {
        if (!string.IsNullOrEmpty(options.ServiceCredential))
        {
            return options.ServiceCredential;
        }

        // 每次调用读取当前请求，不在共享客户端或 Provider 字段中保存用户令牌。
        var headers = _httpContextAccessor.HttpContext?.Request.Headers.Authorization;
        if (headers is not { Count: 1 } ||
            !AuthenticationHeaderValue.TryParse(headers.Value[0], out var authorization) ||
            !string.Equals(authorization.Scheme, "Bearer", StringComparison.OrdinalIgnoreCase) ||
            !IsBearerToken(authorization.Parameter))
        {
            return null;
        }

        return authorization.Parameter;
    }

    private static bool IsBearerToken(string? token)
    {
        if (string.IsNullOrEmpty(token))
        {
            return false;
        }

        var paddingStart = token.IndexOf('=');
        var valueLength = paddingStart < 0 ? token.Length : paddingStart;
        if (valueLength == 0)
        {
            return false;
        }

        for (var index = 0; index < valueLength; index++)
        {
            var character = token[index];
            if (!char.IsAsciiLetterOrDigit(character) && character is not ('-' or '.' or '_' or '~' or '+' or '/'))
            {
                return false;
            }
        }

        for (var index = valueLength; index < token.Length; index++)
        {
            if (token[index] != '=')
            {
                return false;
            }
        }

        return true;
    }

    private static async Task<byte[]?> ReadResponseAsync(
        Stream stream,
        CancellationToken cancellationToken)
    {
        var buffer = ArrayPool<byte>.Shared.Rent(16 * 1024);
        try
        {
            using var result = new MemoryStream();
            var total = 0;
            while (true)
            {
                var remaining = MaxResponseBytes - total;
                var readSize = Math.Min(buffer.Length, remaining + 1);
                var read = await stream.ReadAsync(
                    buffer.AsMemory(0, readSize),
                    cancellationToken);
                if (read == 0)
                {
                    return result.ToArray();
                }

                total += read;
                if (total > MaxResponseBytes)
                {
                    return null;
                }

                result.Write(buffer, 0, read);
            }
        }
        finally
        {
            ArrayPool<byte>.Shared.Return(buffer);
        }
    }

    private AuthorizationDecision ParseDecision(ReadOnlySpan<byte> responseBytes)
    {
        try
        {
            using var document = JsonDocument.Parse(responseBytes.ToArray());
            if (document.RootElement.ValueKind != JsonValueKind.Object)
            {
                return Reject("invalid_remote_response");
            }

            JsonElement? allowed = null;
            JsonElement? effect = null;
            JsonElement? reason = null;

            foreach (var property in document.RootElement.EnumerateObject())
            {
                if (property.NameEquals("allowed"))
                {
                    if (allowed is not null)
                    {
                        return Reject("invalid_remote_response");
                    }

                    allowed = property.Value.Clone();
                }
                else if (property.NameEquals("effect"))
                {
                    if (effect is not null)
                    {
                        return Reject("invalid_remote_response");
                    }

                    effect = property.Value.Clone();
                }
                else if (property.NameEquals("reason"))
                {
                    if (reason is not null)
                    {
                        return Reject("invalid_remote_response");
                    }

                    reason = property.Value.Clone();
                }
            }

            if (allowed is null ||
                effect is null ||
                reason is null ||
                allowed.Value.ValueKind is not (JsonValueKind.True or JsonValueKind.False) ||
                effect.Value.ValueKind != JsonValueKind.String ||
                reason.Value.ValueKind != JsonValueKind.String)
            {
                return Reject("invalid_remote_response");
            }

            var isAllowed = allowed.Value.GetBoolean();
            var effectValue = effect.Value.GetString();
            var reasonValue = reason.Value.GetString();
            if (string.IsNullOrEmpty(reasonValue))
            {
                return Reject("invalid_remote_response");
            }

            if (isAllowed &&
                effectValue == "allow" &&
                reasonValue == "allowed")
            {
                return new AuthorizationDecision(true, "allow", "allowed");
            }

            if (!isAllowed &&
                effectValue == "deny" &&
                reasonValue != "allowed")
            {
                return new AuthorizationDecision(false, "deny", reasonValue);
            }

            return Reject("invalid_remote_response");
        }
        catch (JsonException)
        {
            return Reject("invalid_remote_response");
        }
    }

    private AuthorizationDecision Reject(string reason)
    {
        _logger.LogWarning(
            "HTTP authorization rejected with reason {Reason}",
            reason);
        return new AuthorizationDecision(false, "deny", reason);
    }

    private sealed record AuthorizationRequest(
        string AppKey,
        string ServiceResource,
        string Subject,
        string Domain,
        string Method,
        string Path);
}
