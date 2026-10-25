using System.Text.Json;
using StackExchange.Redis;

namespace Demo.Api;

// 每次从业务 key 读取完整策略；没有独立发布状态，也不缓存授权结果。
public sealed class RedisAuthorizationStore(
    IConnectionMultiplexer connection,
    DemoSettings settings,
    ILogger<RedisAuthorizationStore> logger)
{
    private const int MaxPolicyBytes = 16 * 1024 * 1024;

    public static void ValidateSettings(DemoSettings settings)
    {
        var redis = settings.Redis;
        if (string.IsNullOrWhiteSpace(redis.Host) || redis.Port is < 1 or > 65535 ||
            redis.Database < 0 || redis.TimeoutMilliseconds is < 1 or > 60000)
            throw new InvalidOperationException("Redis 的地址、端口、数据库编号或超时配置无效。");
        if (string.IsNullOrWhiteSpace(redis.KeyPrefix) || redis.KeyPrefix.Any(char.IsWhiteSpace) ||
            redis.KeyPrefix.IndexOfAny(['{', '}']) >= 0 || redis.KeyPrefix.EndsWith(':'))
            throw new InvalidOperationException("Redis:KeyPrefix 不能为空、包含空白或花括号，或以冒号结尾。");
        if (string.IsNullOrWhiteSpace(settings.AuthorizationCenter.ServiceResource))
            throw new InvalidOperationException("必须配置 AuthorizationCenter:ServiceResource。");
    }

    // Go url.QueryEscape 使用 '+' 表示空格，其余按 UTF-8 百分号编码。
    public static string QueryEscape(string value) => Uri.EscapeDataString(value).Replace("%20", "+");

    public static string NormalizeDomain(string? value)
    {
        var domain = value?.Trim() ?? "";
        return domain == "global" ? "" : domain;
    }

    public async Task<AuthorizationDecision> AuthorizeAsync(
        string subject, string method, string path, CancellationToken cancellationToken)
    {
        var center = settings.AuthorizationCenter;
        var service = center.ServiceResource.Trim();
        var domain = NormalizeDomain(center.Domain);
        subject = subject.Trim();
        if (subject.Length == 0)
            return Deny("subject_required");

        var policyKey = $"{settings.Redis.KeyPrefix}:{QueryEscape(service)}:{QueryEscape(subject)}";
        if (domain.Length != 0)
            policyKey += $":{QueryEscape(domain)}";

        try
        {
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(settings.Redis.TimeoutMilliseconds);
            var value = await connection.GetDatabase(settings.Redis.Database)
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
                policy.ServiceResource != service || NormalizeDomain(policy.Domain) != domain)
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

    private static AuthorizationDecision Deny(string reason) => new(false, "deny", reason);
}
