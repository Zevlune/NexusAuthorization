using System.Text;
using Demo.Api;
using NexusAuthorization.Sdk;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;

namespace Demo.Api.Controllers;

[ApiController]
[Route("api/demo/reports")]
[NexusAuthorize]
[Tags("Reports")]
public sealed class ReportsController : ControllerBase
{
    [HttpGet("summary")]
    [EndpointSummary("报表汇总：返回固定演示统计")]
    [ProducesResponseType(typeof(ReportSummaryResponse), StatusCodes.Status200OK)]
    public IActionResult GetSummary()
    {
        return Ok(new ReportSummaryResponse(2, 298.00m, 2));
    }

    [HttpPost("export")]
    [Produces("text/csv")]
    [EndpointSummary("导出订单：下载固定演示 CSV")]
    [ProducesResponseType(typeof(FileContentResult), StatusCodes.Status200OK)]
    public IActionResult Export()
    {
        const string csv = "id,number,amount\r\n1,DEMO-001,99.00\r\n2,DEMO-002,199.00\r\n";
        var content = Encoding.UTF8.GetBytes(csv);
        return File(content, "text/csv", "demo-orders.csv");
    }
}

public sealed record ReportSummaryResponse(int OrderCount, decimal TotalAmount, int ProductCount);
