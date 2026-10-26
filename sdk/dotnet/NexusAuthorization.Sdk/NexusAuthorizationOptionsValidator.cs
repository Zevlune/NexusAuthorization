using Microsoft.Extensions.Options;

namespace NexusAuthorization.Sdk;

internal sealed class NexusAuthorizationOptionsValidator
    : IValidateOptions<NexusAuthorizationOptions>
{
    public ValidateOptionsResult Validate(string? name, NexusAuthorizationOptions options)
    {
        if (string.IsNullOrWhiteSpace(options.ServiceResource))
            return ValidateOptionsResult.Fail("必须配置 NexusAuthorization:ServiceResource。");
        if (string.IsNullOrWhiteSpace(options.SubjectClaimType))
            return ValidateOptionsResult.Fail("NexusAuthorization:SubjectClaimType 不能为空。");
        return ValidateOptionsResult.Success;
    }
}
