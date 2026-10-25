using System.Security.Claims;
using Microsoft.AspNetCore.Authorization;

namespace Demo.Api;

public sealed class NexusAuthorizationRequirement : IAuthorizationRequirement;

// 身份仍由 NexusAuth 验证；业务权限从 NexusAuthorization 发布的 Redis 策略读取。
public sealed class NexusAuthorizationHandler(
    RedisAuthorizationStore policies,
    ILogger<NexusAuthorizationHandler> logger) : AuthorizationHandler<NexusAuthorizationRequirement>
{
    protected override async Task HandleRequirementAsync(
        AuthorizationHandlerContext context, NexusAuthorizationRequirement requirement)
    {
        if (context.Resource is not HttpContext httpContext || context.User.Identity?.IsAuthenticated != true)
            return;

        var subject = context.User.FindFirstValue("sub");
        if (string.IsNullOrWhiteSpace(subject))
        {
            logger.LogWarning("Redis 鉴权失败：已认证用户缺少 sub。");
            context.Fail();
            return;
        }

        var decision = await policies.AuthorizeAsync(subject, httpContext.Request.Method,
            httpContext.Request.Path.Value ?? "/", httpContext.RequestAborted);
        logger.LogInformation("Redis 鉴权：{Method} {Path}，Allowed={Allowed}，Reason={Reason}",
            httpContext.Request.Method, httpContext.Request.Path.Value, decision.Allowed, decision.Reason);

        if (decision is { Allowed: true, Effect: "allow", Reason: "allowed" })
            context.Succeed(requirement);
        else
            context.Fail();
    }
}
