using Microsoft.Extensions.DependencyInjection;

namespace NexusAuthorization.Sdk.Http;

public static class HttpBuilderExtensions
{
    public static void UseHttp(
        this NexusAuthorizationBuilder builder,
        Action<HttpAuthorizationOptions> configure)
    {
        ArgumentNullException.ThrowIfNull(builder);
        ArgumentNullException.ThrowIfNull(configure);
        builder.UseProvider<HttpAuthorizationProvider>();

        var services = builder.Services;
        services.AddOptions<HttpAuthorizationOptions>()
            .Configure(configure);
        services.AddHttpContextAccessor();
        services.AddHttpClient(
                HttpAuthorizationProvider.HttpClientName,
                client => client.Timeout = Timeout.InfiniteTimeSpan)
            .ConfigurePrimaryHttpMessageHandler(() => new HttpClientHandler
            {
                AllowAutoRedirect = false,
                UseCookies = false
            })
            .RemoveAllLoggers();
    }
}
