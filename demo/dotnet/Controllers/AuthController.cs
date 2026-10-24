using System.Security.Claims;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;
using Microsoft.AspNetCore.WebUtilities;

namespace Demo.Api.Controllers;

[ApiController]
[Route("api/auth")]
[Tags("Auth")]
public sealed class AuthController(DemoSettings settings) : ControllerBase
{
    [AllowAnonymous]
    [HttpGet("login")]
    [EndpointSummary("在浏览器直接打开此地址，跳转 NexusAuth 登录")]
    [ProducesResponseType(StatusCodes.Status302Found)]
    [ProducesResponseType<ProblemDetails>(StatusCodes.Status503ServiceUnavailable)]
    public IActionResult Login()
    {
        if (string.IsNullOrWhiteSpace(settings.Auth.ClientSecret))
            return Problem("请先配置 Auth:ClientSecret 并在 NexusAuth 登记客户端。",
                statusCode: StatusCodes.Status503ServiceUnavailable);

        return Challenge(new AuthenticationProperties { RedirectUri = "/swagger/index.html" },
            DemoAuthenticationDefaults.OidcScheme);
    }

    [Authorize]
    [HttpGet("me")]
    [EndpointSummary("查看当前已认证用户，不调用权限中心")]
    [ProducesResponseType(StatusCodes.Status200OK)]
    [ProducesResponseType(StatusCodes.Status401Unauthorized)]
    public IActionResult Me() => Ok(new
    {
        subject = User.FindFirstValue("sub"),
        name = User.Identity?.Name,
        claims = User.Claims.Select(claim => new { claim.Type, claim.Value })
    });

    [AllowAnonymous]
    [HttpPost("logout")]
    [EndpointSummary("清除本地 Cookie，打开返回的 logoutUrl 完成统一登出")]
    [ProducesResponseType(StatusCodes.Status200OK)]
    public async Task<IActionResult> Logout()
    {
        var idToken = await HttpContext.GetTokenAsync(DemoAuthenticationDefaults.SessionScheme, "id_token");
        await HttpContext.SignOutAsync(DemoAuthenticationDefaults.SessionScheme);
        var logoutUrl = string.IsNullOrWhiteSpace(idToken) ? "" : QueryHelpers.AddQueryString(
            $"{settings.Auth.Authority.TrimEnd('/')}/connect/endsession", new Dictionary<string, string?>
            {
                ["id_token_hint"] = idToken,
                ["post_logout_redirect_uri"] = settings.Auth.PostLogoutRedirectUri
            });
        return Ok(new { logoutUrl });
    }
}
