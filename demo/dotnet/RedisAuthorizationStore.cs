using System.Text.Json;
using StackExchange.Redis;

namespace Demo.Api;

// 只读取授权中心发布的记录；每次原子读取闸门和策略，不缓存旧的授权结果。
public sealed class RedisAuthorizationStore(
    IConnectionMultiplexer connection,
    DemoSettings settings,
    ILogger<RedisAuthorizationStore> logger)
{
    private const int MaxPolicyBytes = 16 * 1024 * 1024;
    private const string LoadPolicyScript = """
        local state = redis.call('GET', KEYS[1])
        if not state then return {0, ''} end
        if state ~= ARGV[1] then return {-1, ''} end
        local payload = redis.call('HGET', KEYS[2], ARGV[2])
        if not payload then return {2, ''} end
        return {1, payload}
        """;

    public static void ValidateSettings(DemoSettings settings)
    {
        var redis = settings.Redis;
        if (string.IsNullOrWhiteSpace(redis.Host) || redis.Port is < 1 or > 65535 ||
            redis.Database < 0 || redis.TimeoutMilliseconds is < 1 or > 60000)
            throw new InvalidOperationException("Redis 的地址、端口、数据库编号或超时配置无效。");
        if (string.IsNullOrWhiteSpace(redis.KeyPrefix) || redis.KeyPrefix.Any(char.IsWhiteSpace) ||
            redis.KeyPrefix.IndexOfAny(['{', '}']) >= 0 || redis.KeyPrefix.EndsWith(':'))
            throw new InvalidOperationException("Redis:KeyPrefix 不能为空、包含空白或花括号，或以冒号结尾。");
        if (string.IsNullOrWhiteSpace(settings.AuthorizationCenter.AppKey) ||
            string.IsNullOrWhiteSpace(settings.AuthorizationCenter.ServiceResource))
            throw new InvalidOperationException("必须配置 AuthorizationCenter:AppKey 和 ServiceResource。");
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
        var appKey = center.AppKey.Trim();
        var service = center.ServiceResource.Trim();
        var domain = NormalizeDomain(center.Domain);
        subject = subject.Trim();
        if (subject.Length == 0)
            return Deny("subject_required");

        var stateKey = $"{settings.Redis.KeyPrefix}:rbac:api:app:{QueryEscape(appKey)}:state";
        var policyKey = $"{settings.Redis.KeyPrefix}:{QueryEscape(service)}:{QueryEscape(subject)}";
        if (domain.Length != 0)
            policyKey += $":{QueryEscape(domain)}";

        try
        {
            using var timeout = CancellationTokenSource.CreateLinkedTokenSource(cancellationToken);
            timeout.CancelAfter(settings.Redis.TimeoutMilliseconds);
            var result = (RedisResult[]?)await connection.GetDatabase(settings.Redis.Database)
                .ScriptEvaluateAsync(LoadPolicyScript, [(RedisKey)stateKey, (RedisKey)policyKey],
                    [(RedisValue)"ready", (RedisValue)appKey]).WaitAsync(timeout.Token);
            if (result is not { Length: 2 })
                return Deny("invalid_policy_result");

            switch ((long)result[0])
            {
                case 0: return Deny("application_not_published");
                case -1: return Deny("application_updating");
                case 2: return Deny("endpoint_not_granted");
                case 1: break;
                default: return Deny("invalid_policy_result");
            }

            var payload = (byte[]?)result[1];
            if (payload is not { Length: > 0 and <= MaxPolicyBytes })
                return Deny("invalid_policy_payload");
            var policy = JsonSerializer.Deserialize<SubjectApiPolicy>(payload);
            if (policy is null)
                return Deny("invalid_policy_payload");
            if (policy.ApplicationKey?.Trim() != appKey || policy.Subject != subject ||
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
