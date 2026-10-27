using System.Globalization;
using System.Security.Claims;
using Demo.Api;
using Microsoft.AspNetCore.Authentication;
using Microsoft.AspNetCore.Authentication.Cookies;
using Microsoft.AspNetCore.Authentication.JwtBearer;
using Microsoft.AspNetCore.Authentication.OpenIdConnect;
using Microsoft.AspNetCore.Authorization;
using Microsoft.AspNetCore.Mvc;
using Microsoft.IdentityModel.Protocols.OpenIdConnect;
using Microsoft.IdentityModel.Tokens;
using Microsoft.OpenApi;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using NexusAuthorization.Sdk.Redis;

var builder = WebApplication.CreateBuilder(args);
var settings = builder.Configuration.Get<DemoSettings>() ?? throw new InvalidOperationException("缺少配置。");
var auth = settings.Auth;
if (auth.TokenEndpointAuthMethod is not ("client_secret_basic" or "client_secret_post"))
    throw new InvalidOperationException(
        "Auth:TokenEndpointAuthMethod 必须为 client_secret_basic 或 client_secret_post。");
builder.Services.AddSingleton(settings);

var authorization = builder.Services.AddNexusAuthorization(settings.ConfigureAuthorization);
switch (settings.AuthorizationCenter.Provider.Trim().ToUpperInvariant())
{
    case "REDIS":
        authorization.UseRedis(settings.ConfigureRedis);
        break;
    case "HTTP":
        authorization.UseHttp(settings.ConfigureHttp);
        break;
    default:
        throw new InvalidOperationException("AuthorizationCenter:Provider 必须为 Redis 或 Http。");
}

builder.Services.AddHttpClient<NexusAuthClient>(client => client.Timeout = TimeSpan.FromSeconds(5));

builder.Services.AddAuthentication("CookieOrBearer")
    .AddPolicyScheme("CookieOrBearer", "Cookie or Bearer", options =>
    {
        options.ForwardDefaultSelector = context => context.Request.Headers.Authorization.ToString()
            .StartsWith("Bearer ", StringComparison.OrdinalIgnoreCase)
                ? JwtBearerDefaults.AuthenticationScheme : DemoAuthenticationDefaults.SessionScheme;
    })
    .AddCookie(DemoAuthenticationDefaults.SessionScheme, options =>
    {
        options.Cookie.Name = ".NexusAuthorization.Demo";
        options.Cookie.HttpOnly = true;
        options.Cookie.SameSite = SameSiteMode.Lax;
        options.Cookie.SecurePolicy = CookieSecurePolicy.SameAsRequest;
        options.SlidingExpiration = false;
        options.Events.OnRedirectToLogin = context =>
        {
            context.Response.StatusCode = StatusCodes.Status401Unauthorized;
            return Task.CompletedTask;
        };
        options.Events.OnRedirectToAccessDenied = context =>
        {
            context.Response.StatusCode = StatusCodes.Status403Forbidden;
            return Task.CompletedTask;
        };
        options.Events.OnValidatePrincipal = async context =>
        {
            var token = context.Properties.GetTokenValue("access_token");
            var client = context.HttpContext.RequestServices.GetRequiredService<NexusAuthClient>();
            if (!await client.IsActiveAsync(token, context.Principal?.FindFirstValue("sub"),
                    context.HttpContext.RequestAborted))
            {
                context.RejectPrincipal();
                await context.HttpContext.SignOutAsync(DemoAuthenticationDefaults.SessionScheme);
            }
        };
    })
    .AddOpenIdConnect(DemoAuthenticationDefaults.OidcScheme, options =>
    {
        options.SignInScheme = DemoAuthenticationDefaults.SessionScheme;
        options.Authority = auth.Authority;
        options.ClientId = auth.ClientId;
        options.ClientSecret = auth.ClientSecret;
        options.BackchannelHttpHandler = new NexusAuthBackchannelHandler(auth);
        options.RequireHttpsMetadata = auth.RequireHttpsMetadata;
        options.ResponseType = OpenIdConnectResponseType.Code;
        options.ResponseMode = OpenIdConnectResponseMode.Query;
        options.UsePkce = true;
        options.SaveTokens = true;
        options.MapInboundClaims = false;
        options.CallbackPath = "/signin-oidc";
        options.Scope.Clear();
        options.Scope.Add("openid");
        options.Scope.Add("profile");
        options.Scope.Add(auth.ApiScope);
        options.TokenValidationParameters.NameClaimType = "name";
        options.TokenValidationParameters.RoleClaimType = "role";
        // query 回调使用 Lax Cookie，允许本地 HTTP 联调。
        options.CorrelationCookie.SameSite = SameSiteMode.Lax;
        options.CorrelationCookie.SecurePolicy = CookieSecurePolicy.SameAsRequest;
        options.NonceCookie.SameSite = SameSiteMode.Lax;
        options.NonceCookie.SecurePolicy = CookieSecurePolicy.SameAsRequest;
        options.Events.OnRedirectToIdentityProvider = context =>
        {
            context.ProtocolMessage.RedirectUri = auth.RedirectUri;
            return Task.CompletedTask;
        };
        options.Events.OnTicketReceived = async context =>
        {
            var client = context.HttpContext.RequestServices.GetRequiredService<NexusAuthClient>();
            if (!await client.IsActiveAsync(context.Properties?.GetTokenValue("access_token"),
                    context.Principal?.FindFirstValue("sub"), context.HttpContext.RequestAborted))
            {
                context.HandleResponse();
                await WriteAuthenticationProblemAsync(context.HttpContext, "登录中心返回了无效的访问令牌。");
                return;
            }

            // Demo 不保存长期会话或刷新令牌；access token 到期后重新登录。
            if (context.Properties is not null && DateTimeOffset.TryParse(
                    context.Properties.GetTokenValue("expires_at"), CultureInfo.InvariantCulture,
                    DateTimeStyles.RoundtripKind, out var expiresAt))
                context.Properties.ExpiresUtc = expiresAt;
        };
        options.Events.OnRemoteFailure = async context =>
        {
            context.HandleResponse();
            await WriteAuthenticationProblemAsync(context.HttpContext,
                "OIDC 登录失败，请检查客户端登记、回调地址和服务端配置。");
        };
    })
    .AddJwtBearer(options =>
    {
        options.Authority = auth.Authority;
        options.Audience = auth.Audience;
        options.RequireHttpsMetadata = auth.RequireHttpsMetadata;
        options.MapInboundClaims = false;
        options.TokenValidationParameters = new TokenValidationParameters
        {
            ValidateIssuer = true,
            ValidIssuer = auth.Authority,
            ValidateAudience = true,
            ValidAudience = auth.Audience,
            ValidateLifetime = true,
            ClockSkew = TimeSpan.FromSeconds(30),
            NameClaimType = "name",
            RoleClaimType = "role"
        };
        options.Events.OnTokenValidated = async context =>
        {
            var client = context.HttpContext.RequestServices.GetRequiredService<NexusAuthClient>();
            var token = context.Request.Headers.Authorization.ToString()["Bearer ".Length..].Trim();
            if (!await client.IsActiveAsync(token, context.Principal?.FindFirstValue("sub"),
                    context.HttpContext.RequestAborted))
                context.Fail("访问令牌已撤销、过期或不属于此客户端/API。");
        };
    });

builder.Services.AddControllers();
builder.Services.AddSwaggerGen(options =>
{
    options.SwaggerDoc("v1", new OpenApiInfo
    {
        Title = "NexusAuthorization .NET 10 Demo",
        Version = "v1",
        Description = "先在浏览器新标签打开 /api/auth/login 完成统一登录，再回来调用接口；也可使用 Authorize 输入 access token。"
    });
    options.AddSecurityDefinition("Bearer", new OpenApiSecurityScheme
    {
        Type = SecuritySchemeType.Http,
        Scheme = "bearer",
        BearerFormat = "JWT",
        Description = "仅输入此 Demo 客户端取得的 access token，不要包含 Bearer 前缀。"
    });
    options.DocumentFilter<SwaggerSecurityFilter>();
});

var app = builder.Build();
app.UseSwagger();
app.UseSwaggerUI();
app.UseAuthentication();
app.UseAuthorization();

app.MapControllers();

app.Run();

static Task WriteAuthenticationProblemAsync(HttpContext context, string detail)
{
    context.Response.StatusCode = StatusCodes.Status401Unauthorized;
    return context.Response.WriteAsJsonAsync(new ProblemDetails
    {
        Status = StatusCodes.Status401Unauthorized,
        Title = "Unauthorized",
        Detail = detail
    }, options: null, contentType: "application/problem+json", cancellationToken: context.RequestAborted);
}

// 供 WebApplicationFactory 启动真实应用管道。
public partial class Program;
