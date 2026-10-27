using System.Text.Json;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using StackExchange.Redis;

namespace NexusAuthorization.Sdk.Redis;

// 每次从业务 key 读取完整策略；没有独立发布状态，也不缓存授权结果。
public sealed class RedisAuthorizationStore(
    IConnectionMultiplexer connection,
    IOptions<NexusAuthorizationOptions> options,
    IOptions<RedisAuthorizationOptions> redisOptions,
    ILogger<RedisAuthorizationStore> logger) : INexusAuthorizationProvider
{
    private readonly NexusAuthorizationOptions common = options.Value;
    private readonly RedisAuthorizationOptions settings = redisOptions.Value;
    private const int MaxPolicyBytes = 16 * 1024 * 1024;

    // Go url.QueryEscape 使用 '+' 表示空格，其余按 UTF-8 百分号编码。
    public static string QueryEscape(string value) => Uri.EscapeDataString(value).Replace("%20", "+");

    public async Task<AuthorizationDecision> AuthorizeAsync(
        string subject, string method, string path, CancellationToken cancellationToken = default)
    {
        var service = common.ServiceResource.Trim();
        if (cancellationToken.IsCancellationRequested)
            return Deny("request_cancelled");
        subject = subject?.Trim() ?? "";
        if (subject.Length == 0)
            return Deny("subject_required");

        var policyKey = $"{settings.KeyPrefix}:{QueryEscape(service)}:{QueryEscape(subject)}";

        try
        {
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(settings.TimeoutMilliseconds);
            var value = await connection.GetDatabase(settings.Database)
                .StringGetAsync(policyKey).WaitAsync(timeout.Token);
            if (value.IsNull)
                return Deny("endpoint_not_granted");

            var payload = (byte[]?)value;
            if (payload is not { Length: > 0 and <= MaxPolicyBytes })
                return Deny("invalid_policy_payload");
            var policy = JsonSerializer.Deserialize<SubjectApiPolicy>(payload);
            if (policy is null)
                return Deny("invalid_policy_payload");
            if (policy.ApplicationKey is { Length: > 0 } ||
                policy.ApplicationId is { } applicationId && applicationId != Guid.Empty)
                return Deny("policy_identity_mismatch");
            if (policy.Subject != subject ||
                policy.ServiceResource != service || !IsDefaultDomain(policy.StoredDomain))
                return Deny("policy_identity_mismatch");

            return SubjectApiPolicyMatcher.Evaluate(policy, method, path);
        }
        catch (OperationCanceledException)
        {
            return Deny(cancellationToken.IsCancellationRequested ? "request_cancelled" : "redis_timeout");
        }
        catch (RedisException)
        {
            // 避免记录包含连接详情或凭据的底层异常；拒绝原因由 Handler 输出。
            logger.LogWarning("Redis 权限记录读取失败，本次请求拒绝访问。");
            return Deny("redis_unavailable");
        }
        catch (Exception exception) when (exception is JsonException or InvalidCastException)
        {
            return Deny("invalid_policy_payload");
        }
    }

    private static bool IsDefaultDomain(string? value) => value?.Trim() is null or "" or "global";

    private static AuthorizationDecision Deny(string reason) => new(false, "deny", reason);
}
