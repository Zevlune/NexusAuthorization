using System.ComponentModel.DataAnnotations;
using Demo.Api;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Http;
using Microsoft.AspNetCore.Mvc;

namespace Demo.Api.Controllers;

[ApiController]
[Route("api/demo/products")]
[Authorize(Policy = DemoAuthenticationDefaults.PermissionPolicy)]
[Tags("Products")]
public sealed class ProductsController : ControllerBase
{
    [HttpGet]
    [EndpointSummary("商品列表：演示固定数据，无持久化")]
    [ProducesResponseType(typeof(IEnumerable<ProductResponse>), StatusCodes.Status200OK)]
    public IActionResult GetProducts()
    {
        return Ok(new[]
        {
            new ProductResponse(1, "演示商品一", 99.00m),
            new ProductResponse(2, "演示商品二", 199.00m)
        });
    }

    [HttpGet("{id:int}")]
    [EndpointSummary("商品详情：演示固定数据，无持久化")]
    [ProducesResponseType(typeof(ProductResponse), StatusCodes.Status200OK)]
    [ProducesResponseType(StatusCodes.Status404NotFound)]
    public IActionResult GetById([FromRoute] int id)
    {
        return id switch
        {
            1 => Ok(new ProductResponse(1, "演示商品一", 99.00m)),
            2 => Ok(new ProductResponse(2, "演示商品二", 199.00m)),
            _ => NotFound()
        };
    }

    [HttpPost]
    [EndpointSummary("创建商品：仅回显并返回 persisted=false，无持久化")]
    [ProducesResponseType(typeof(ProductMutationResponse), StatusCodes.Status201Created)]
    [ProducesResponseType(StatusCodes.Status400BadRequest)]
    public IActionResult Create([FromBody] ProductRequest request)
    {
        var product = new ProductMutationResponse(3, request.Name, request.Price, false);
        return CreatedAtAction(nameof(GetById), new { id = product.Id }, product);
    }

    [HttpPut("{id:int}")]
    [EndpointSummary("更新商品：仅回显并返回 persisted=false，无持久化")]
    [ProducesResponseType(typeof(ProductMutationResponse), StatusCodes.Status200OK)]
    [ProducesResponseType(StatusCodes.Status400BadRequest)]
    [ProducesResponseType(StatusCodes.Status404NotFound)]
    public IActionResult Update([FromRoute] int id, [FromBody] ProductRequest request)
    {
        if (id is not (1 or 2))
        {
            return NotFound();
        }

        return Ok(new ProductMutationResponse(id, request.Name, request.Price, false));
    }

    [HttpDelete("{id:int}")]
    [EndpointSummary("删除商品：仅返回演示结果，无持久化")]
    [ProducesResponseType(typeof(DeleteProductResponse), StatusCodes.Status200OK)]
    [ProducesResponseType(StatusCodes.Status404NotFound)]
    public IActionResult Delete([FromRoute] int id)
    {
        if (id is not (1 or 2))
        {
            return NotFound();
        }

        return Ok(new DeleteProductResponse(id, false, "演示删除，不保存数据"));
    }
}

public sealed class ProductRequest
{
    [Required(ErrorMessage = "Name 为必填项。")]
    [StringLength(100, ErrorMessage = "Name 长度不能超过 100 个字符。")]
    public string Name { get; init; } = "";

    [Range(typeof(decimal), "0.01", "1000000",
        ErrorMessage = "Price 必须在 0.01 到 1000000 之间。")]
    public decimal Price { get; init; }
}

public sealed record ProductResponse(int Id, string Name, decimal Price);

public sealed record ProductMutationResponse(int Id, string Name, decimal Price, bool Persisted);

public sealed record DeleteProductResponse(int Id, bool Persisted, string Message);
