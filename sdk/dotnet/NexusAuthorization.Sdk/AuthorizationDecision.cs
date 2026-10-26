namespace NexusAuthorization.Sdk;

public sealed record AuthorizationDecision(bool Allowed, string Effect, string Reason);
