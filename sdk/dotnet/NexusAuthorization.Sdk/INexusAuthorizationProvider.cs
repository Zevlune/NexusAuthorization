namespace NexusAuthorization.Sdk;

/// <summary>权限检查入口；身份认证由接入项目负责。</summary>
public interface INexusAuthorizationProvider
{
    Task<AuthorizationDecision> AuthorizeAsync(string subject, string method, string path,
        CancellationToken cancellationToken = default);
}
