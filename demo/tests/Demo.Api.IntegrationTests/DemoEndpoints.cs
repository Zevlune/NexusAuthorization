using System.Net;

namespace Demo.Api.IntegrationTests;

public static class TestIdentity
{
    public const string Subject = "51b21204-54ab-4da6-ad83-d14f72ced46f";
}

public sealed record DemoEndpoint(string Method, string Path, string Template, int SuccessStatus);

public static class DemoEndpoints
{
    public static IReadOnlyList<DemoEndpoint> All { get; } =
    [
        new("GET", "/api/demo/orders", "/api/demo/orders", (int)HttpStatusCode.OK),
        new("GET", "/api/demo/orders/1", "/api/demo/orders/{id}", (int)HttpStatusCode.OK),
        new("GET", "/api/demo/products", "/api/demo/products", (int)HttpStatusCode.OK),
        new("GET", "/api/demo/products/1", "/api/demo/products/{id}", (int)HttpStatusCode.OK),
        new("POST", "/api/demo/products", "/api/demo/products", (int)HttpStatusCode.Created),
        new("PUT", "/api/demo/products/1", "/api/demo/products/{id}", (int)HttpStatusCode.OK),
        new("DELETE", "/api/demo/products/1", "/api/demo/products/{id}", (int)HttpStatusCode.OK),
        new("GET", "/api/demo/reports/summary", "/api/demo/reports/summary", (int)HttpStatusCode.OK),
        new("POST", "/api/demo/reports/export", "/api/demo/reports/export", (int)HttpStatusCode.OK)
    ];

    public static IEnumerable<object[]> Cases =>
        All.Select(endpoint => new object[]
        {
            endpoint.Method,
            endpoint.Path,
            endpoint.Template,
            endpoint.SuccessStatus
        });
}
