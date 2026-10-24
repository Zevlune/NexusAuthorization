using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;

namespace Demo.Api.Controllers;

[ApiController]
[Route("api/public")]
[AllowAnonymous]
[Tags("Public")]
public sealed class PublicController : ControllerBase
{
    [HttpGet("ping")]
    [EndpointSummary("匿名健康检查")]
    [ProducesResponseType(typeof(PingResponse), StatusCodes.Status200OK)]
    public IActionResult Ping()
    {
        return Ok(new PingResponse("pong"));
    }

    [HttpGet("info")]
    [EndpointSummary("匿名读取 Demo 基本信息")]
    [ProducesResponseType(typeof(DemoInfoResponse), StatusCodes.Status200OK)]
    public IActionResult Info()
    {
        return Ok(new DemoInfoResponse("NexusAuthorization Demo", ".NET 10", "none"));
    }
}

public sealed record PingResponse(string Message);

public sealed record DemoInfoResponse(string Name, string Framework, string Storage);
