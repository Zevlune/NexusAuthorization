# API token authorization client

`apitoken` is a small Go client for `POST /internal/v1/api-tokens/authorize`.
It sends the API token in the JSON body and authenticates the calling service
with the independently configured `X-Internal-Service-Key` header. The client
does not cache decisions.

```go
client := apitoken.NewClient(apitoken.Options{
	BaseURL:    "https://nexus-authorization.internal",
	ServiceKey: os.Getenv("NEXUSAUTH_INTERNAL_SERVICE_KEY"), // load at startup
})

target := apitoken.Target{
	AppKey:          "orders",
	ServiceResource: "orders-api",
	Method:          http.MethodPost,
	Path:            "/v1/orders/42",
	TenantID:        trustedTenantID,
	ProjectID:       trustedProjectID,
}
decision, err := client.Authorize(ctx, rawToken, target)
if err != nil || !decision.Allowed {
	// Fail closed when the authorization service is unavailable or denies.
	return errForbidden
}
```

The caller must resolve `TenantID` and `ProjectID` from its trusted
authentication and routing context, independently of untrusted request body
scope. It must also enforce its own row-level filtering after authorization.
The internal service key is separate from `rawToken` and should be loaded at
startup. Restart the caller when that credential changes; database API-token
revocation takes effect on the next uncached authorization call.

The default HTTP timeout is three seconds. A custom `*http.Client` may be
provided through `Options.HTTPClient`; the client value is copied, its
transport is preserved, and cross-host redirects are rejected before the
internal key can be sent to another host. `https` is required for non-local
hosts. Plain `http` is accepted for localhost/loopback development and tests.
