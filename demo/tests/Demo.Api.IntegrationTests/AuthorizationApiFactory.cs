using System.Security.Claims;
using System.Text.Encodings.Web;
using Demo.Api;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Hosting;
using Microsoft.AspNetCore.Mvc.Testing;
using Microsoft.AspNetCore.TestHost;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.DependencyInjection.Extensions;
using Microsoft.Extensions.Logging;
using Microsoft.Extensions.Options;

namespace Demo.Api.IntegrationTests;

// 仅替换登录身份来源；保留应用的 DI、授权策略、Redis String 读取和真实 Controller。
public sealed class AuthorizationApiFactory(DemoSettings settings) : WebApplicationFactory<Program>
{
    public DemoSettings Settings { get; } = settings;

    protected override void ConfigureWebHost(IWebHostBuilder builder)
    {
        builder.UseEnvironment("Testing");
        builder.UseContentRoot(DemoProjectPaths.ApiDirectory);
        builder.ConfigureTestServices(services =>
        {
            services.RemoveAll<DemoSettings>();
            services.AddSingleton(Settings);
            services.AddAuthentication(options =>
            {
                options.DefaultScheme = TestAuthenticationHandler.SchemeName;
                options.DefaultAuthenticateScheme = TestAuthenticationHandler.SchemeName;
                options.DefaultChallengeScheme = TestAuthenticationHandler.SchemeName;
                options.DefaultForbidScheme = TestAuthenticationHandler.SchemeName;
            }).AddScheme<AuthenticationSchemeOptions, TestAuthenticationHandler>(
                TestAuthenticationHandler.SchemeName, _ => { });
        });
    }

    public HttpClient CreateAuthenticatedClient(string? subject = TestIdentity.Subject)
    {
        var client = CreateClient(new WebApplicationFactoryClientOptions { AllowAutoRedirect = false });
        client.DefaultRequestHeaders.Add("X-Test-Authenticated", "true");
        if (subject is not null)
            client.DefaultRequestHeaders.Add("X-Test-Subject", subject);
        return client;
    }
}

// 该认证方案只存在于测试程序集，生产应用不识别这些测试请求头。
internal sealed class TestAuthenticationHandler(
    IOptionsMonitor<AuthenticationSchemeOptions> options,
    ILoggerFactory logger,
    UrlEncoder encoder) : AuthenticationHandler<AuthenticationSchemeOptions>(options, logger, encoder)
{
    public const string SchemeName = "IntegrationTest";

    protected override Task<AuthenticateResult> HandleAuthenticateAsync()
    {
        if (Request.Headers["X-Test-Authenticated"] != "true")
            return Task.FromResult(AuthenticateResult.NoResult());

        var claims = new List<Claim>();
        var subject = Request.Headers["X-Test-Subject"].ToString();
        if (subject.Length != 0)
            claims.Add(new Claim("sub", subject));
        var principal = new ClaimsPrincipal(new ClaimsIdentity(claims, SchemeName));
        return Task.FromResult(AuthenticateResult.Success(new AuthenticationTicket(principal, SchemeName)));
    }
}

internal static class DemoProjectPaths
{
    public static string ApiDirectory
    {
        get
        {
            for (var directory = new DirectoryInfo(AppContext.BaseDirectory);
                 directory is not null; directory = directory.Parent)
            {
                var candidate = Path.Combine(directory.FullName, "dotnet");
                if (File.Exists(Path.Combine(candidate, "Demo.Api.csproj")))
                    return candidate;
            }
            throw new InvalidOperationException("找不到 demo/dotnet/Demo.Api.csproj，请在仓库中运行测试。");
        }
    }
}
