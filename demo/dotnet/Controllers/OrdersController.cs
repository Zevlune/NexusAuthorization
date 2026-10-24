using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;

namespace Demo.Api.Controllers;

[ApiController]
[Authorize(Policy = DemoAuthenticationDefaults.PermissionPolicy)]
[Route("api/demo/orders")]
[Tags("Orders")]
[ProducesResponseType(StatusCodes.Status401Unauthorized)]
[ProducesResponseType(StatusCodes.Status403Forbidden)]
public sealed class OrdersController : ControllerBase
{
    [HttpGet]
    [EndpointSummary("订单列表：登录认证 + Redis 权限校验")]
    [ProducesResponseType<DemoOrder[]>(StatusCodes.Status200OK)]
    public ActionResult<DemoOrder[]> List() => Ok(new[]
    {
        new DemoOrder(1, "DEMO-001", 99.00m),
        new DemoOrder(2, "DEMO-002", 199.00m)
    });

    [HttpGet("{id:int}")]
    [EndpointSummary("订单详情：演示参数路径鉴权")]
    [ProducesResponseType<DemoOrder>(StatusCodes.Status200OK)]
    [ProducesResponseType(StatusCodes.Status404NotFound)]
    public ActionResult<DemoOrder> GetById(int id)
    {
        if (id is not (1 or 2))
            return NotFound();

        return Ok(new DemoOrder(id, $"DEMO-{id:000}", id == 1 ? 99.00m : 199.00m));
    }
}

public sealed record DemoOrder(int Id, string Number, decimal Amount);
