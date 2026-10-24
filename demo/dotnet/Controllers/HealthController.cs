using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;

namespace Demo.Api.Controllers;

[ApiController]
[AllowAnonymous]
[Route("health")]
[Tags("Health")]
public sealed class HealthController : ControllerBase
{
    [HttpGet]
    [EndpointSummary("检查 Demo 是否正常启动，无需连接登录中心和权限中心")]
    [ProducesResponseType(StatusCodes.Status200OK)]
    public IActionResult Get() => Ok(new { status = "ok" });
}
