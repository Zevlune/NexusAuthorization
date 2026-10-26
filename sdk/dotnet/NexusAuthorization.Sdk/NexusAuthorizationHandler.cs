using System.Security.Claims;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc.Filters;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;
using Microsoft.AspNetCore.Authorization;

namespace NexusAuthorization.Sdk;

// 身份由接入项目验证；业务权限由配置的 Provider 检查。
internal sealed class NexusAuthorizationHandler(
    INexusAuthorizationProvider policies,
    IOptions<NexusAuthorizationOptions> options,
    ILogger<NexusAuthorizationHandler> logger) : AuthorizationHandler<NexusAuthorizationRequirement>
{
    protected override async Task HandleRequirementAsync(
        AuthorizationHandlerContext context, NexusAuthorizationRequirement requirement)
    {
        var httpContext = context.Resource switch
        {
            HttpContext http => http,
            AuthorizationFilterContext filter => filter.HttpContext,
            _ => null
        };
        if (httpContext is null || context.User.Identity?.IsAuthenticated != true)
            return;

        var subject = context.User.FindFirstValue(options.Value.SubjectClaimType);
        if (string.IsNullOrWhiteSpace(subject))
        {
            logger.LogWarning("权限检查失败：已认证用户缺少配置的主体声明。");
            context.Fail();
            return;
        }

        var decision = await policies.AuthorizeAsync(subject, httpContext.Request.Method,
            httpContext.Request.Path.Value ?? "/", httpContext.RequestAborted);
        logger.LogInformation("权限检查：{Method} {Path}，Allowed={Allowed}，Reason={Reason}",
            httpContext.Request.Method, httpContext.Request.Path.Value, decision.Allowed, decision.Reason);

        if (decision is { Allowed: true, Effect: "allow", Reason: "allowed" })
            context.Succeed(requirement);
        else
            context.Fail();
    }
}
