namespace NexusAuthorization.Sdk;

/// <summary>各实现共享的授权上下文。连接配置由相应实现库提供。</summary>
public sealed class NexusAuthorizationOptions
{
    public const string SectionName = "NexusAuthorization";

    public string ServiceResource { get; set; } = "";
    public string Domain { get; set; } = "";
    public string SubjectClaimType { get; set; } = "sub";
}
