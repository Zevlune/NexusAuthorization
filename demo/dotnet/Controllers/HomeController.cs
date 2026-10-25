using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;

namespace Demo.Api.Controllers;

[ApiController]
[AllowAnonymous]
[Route("")]
[ApiExplorerSettings(IgnoreApi = true)]
public sealed class HomeController : ControllerBase
{
    [HttpGet]
    public IActionResult Index() => Redirect("/swagger");
}
