using Microsoft.AspNetCore.Authorization;

namespace NexusAuthorization.Sdk;

public static class NexusAuthorizationDefaults
{
    public const string PolicyName = "NexusAuthorization";
}

/// <summary>要求已认证，并依据 SDK 配置检查当前 HTTP 方法和路径的权限。</summary>
public sealed class NexusAuthorizeAttribute : AuthorizeAttribute
{
    public NexusAuthorizeAttribute() => Policy = NexusAuthorizationDefaults.PolicyName;
}
