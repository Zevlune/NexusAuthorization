using Microsoft.AspNetCore.Authorization;
using Microsoft.OpenApi;
using Swashbuckle.AspNetCore.SwaggerGen;

namespace Demo.Api;

public sealed class SwaggerSecurityFilter : IDocumentFilter
{
    public void Apply(OpenApiDocument document, DocumentFilterContext context)
    {
        foreach (var api in context.ApiDescriptions)
        {
            var metadata = api.ActionDescriptor.EndpointMetadata;
            if (!metadata.OfType<IAuthorizeData>().Any() || metadata.OfType<IAllowAnonymous>().Any())
                continue;

            var path = "/" + api.RelativePath;
            if (!document.Paths.TryGetValue(path, out var item) || item.Operations is null)
                continue;

            foreach (var operation in item.Operations.Where(pair =>
                pair.Key.ToString().Equals(api.HttpMethod, StringComparison.OrdinalIgnoreCase)))
            {
                operation.Value.Security =
                [
                    new OpenApiSecurityRequirement
                    {
                        [new OpenApiSecuritySchemeReference("Bearer", document)] = []
                    }
                ];
            }
        }
    }
}
