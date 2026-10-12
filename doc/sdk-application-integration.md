# SDK application integration

The Go SDK now requires every authorization check to identify both the trusted business application and the service resource being accessed. This is a breaking change for callers of `Client.Check` and `Client.Require`.

`Request.AppKey` and `Request.ServiceResource` are required. `AppKey` must be the configured application key for the calling service, and `ServiceResource` must identify the protected API resource. Do not read either value from request query parameters, headers, or other user-controlled input.

```go
client := rbac.NewClient("https://permission-center.example", os.Getenv("NEXUSAUTHORIZATION_CREDENTIAL"))

middleware := client.Require(
	"books",
	"book-service",
	func(r *http.Request) (rbac.Identity, bool) {
		// Resolve identity from trusted authentication middleware.
		identity, ok := auth.IdentityFromContext(r.Context())
		return rbac.Identity{Subject: identity.Subject, Domain: identity.Domain}, ok
	},
	http.HandlerFunc(listBooks),
)
http.Handle("/api/books", middleware)
```

For direct checks, include both values in the request:

```go
decision, err := client.Check(ctx, rbac.Request{
	AppKey:          "books",
	ServiceResource: "book-service",
	Subject:         "user:1",
	Domain:          "global",
	Method:          http.MethodGet,
	Path:            "/api/books",
})
```

The middleware fails closed: missing application or service configuration, an unauthenticated identity, authorization service errors, malformed decisions, and denied decisions prevent the downstream handler from running.
