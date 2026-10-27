using System.Net;
using System.Text;
using System.Text.Json;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.Logging.Abstractions;
using Microsoft.Extensions.Options;
using Microsoft.Extensions.Primitives;
using NexusAuthorization.Sdk;
using NexusAuthorization.Sdk.Http;
using Xunit;

namespace NexusAuthorization.Sdk.Tests;

public sealed class HttpAuthorizationProviderTests
{
    private const string AllowedJson =
        """{"allowed":true,"effect":"allow","reason":"allowed","matched_roles":["reader"]}""";

    [Theory]
    [InlineData("")]
    [InlineData(null)]
    public async Task AuthorizeAsync_DefaultEmptyOrNullCredentialForwardsCurrentContextBearer(
        string? serviceCredential)
    {
        using var handler = new StubHttpMessageHandler((request, _) =>
        {
            Assert.Equal("Bearer forwarded-token", request.Headers.Authorization?.ToString());
            return Task.FromResult(JsonResponse(AllowedJson));
        });
        var provider = CreateProvider(
            new StubHttpClientFactory(handler),
            serviceCredential: serviceCredential,
            httpContextAccessor: CreateAccessor("Bearer forwarded-token"));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(new AuthorizationDecision(true, "allow", "allowed"), decision);
    }

    [Fact]
    public async Task AuthorizeAsync_RefreshesDefaultBearerTokenForEachRequest()
    {
        var authorizations = new List<string?>();
        using var handler = new StubHttpMessageHandler((request, _) =>
        {
            authorizations.Add(request.Headers.Authorization?.ToString());
            return Task.FromResult(JsonResponse(AllowedJson));
        });
        var accessor = CreateAccessor("Bearer first-token");
        var provider = CreateProvider(
            new StubHttpClientFactory(handler),
            serviceCredential: "",
            httpContextAccessor: accessor);

        await provider.AuthorizeAsync("user-1", "GET", "/orders/1");
        accessor.HttpContext!.Request.Headers["Authorization"] = "Bearer second-token";
        await provider.AuthorizeAsync("user-1", "GET", "/orders/2");

        Assert.Equal(
            new[] { "Bearer first-token", "Bearer second-token" },
            authorizations);
    }

    [Theory]
    [InlineData("Bearer", "Az09-._~+/")]
    [InlineData("bearer", "Az09-._~+/=")]
    [InlineData("BEARER", "padded=")]
    public async Task AuthorizeAsync_AcceptsCaseInsensitiveBearerAndToken68(
        string scheme,
        string token)
    {
        using var handler = new StubHttpMessageHandler((request, _) =>
        {
            Assert.Equal($"Bearer {token}", request.Headers.Authorization?.ToString());
            return Task.FromResult(JsonResponse(AllowedJson));
        });
        var provider = CreateProvider(
            new StubHttpClientFactory(handler),
            serviceCredential: "",
            httpContextAccessor: CreateAccessor($"{scheme} {token}"));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(new AuthorizationDecision(true, "allow", "allowed"), decision);
    }

    [Theory]
    [InlineData(false, null)]
    [InlineData(true, null)]
    [InlineData(true, "")]
    [InlineData(true, "Basic credentials")]
    [InlineData(true, "Bearer")]
    [InlineData(true, "Bearer ")]
    [InlineData(true, "Bearer token extra")]
    [InlineData(true, "Bearer token=extra")]
    [InlineData(true, "Bearer =token")]
    [InlineData(true, "Bearer token!")]
    [InlineData(true, "Bearer caf\u00e9")]
    public async Task AuthorizeAsync_RejectsMissingContextOrInvalidAuthorizationBeforeRemote(
        bool hasContext,
        string? authorization)
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            throw new InvalidOperationException("handler should not run"));
        var factory = new StubHttpClientFactory(handler);
        var accessor = new HttpContextAccessor();
        if (hasContext)
        {
            accessor.HttpContext = new DefaultHttpContext();
            if (authorization is not null)
            {
                accessor.HttpContext.Request.Headers["Authorization"] = authorization;
            }
        }

        var provider = CreateProvider(
            factory,
            serviceCredential: "",
            httpContextAccessor: accessor);

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "bearer_token_required"),
            decision);
        Assert.Null(factory.RequestedClientName);
    }

    [Fact]
    public async Task AuthorizeAsync_RejectsMultipleAuthorizationHeadersBeforeRemote()
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            throw new InvalidOperationException("handler should not run"));
        var factory = new StubHttpClientFactory(handler);
        var accessor = CreateAccessor("Bearer first-token", "Bearer second-token");
        var provider = CreateProvider(
            factory,
            serviceCredential: null,
            httpContextAccessor: accessor);

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "bearer_token_required"),
            decision);
        Assert.Null(factory.RequestedClientName);
    }

    [Fact]
    public async Task AuthorizeAsync_ExplicitCredentialOverridesCurrentContextBearer()
    {
        using var handler = new StubHttpMessageHandler((request, _) =>
        {
            Assert.Equal("Bearer service-secret", request.Headers.Authorization?.ToString());
            return Task.FromResult(JsonResponse(AllowedJson));
        });
        var provider = CreateProvider(
            new StubHttpClientFactory(handler),
            serviceCredential: "service-secret",
            httpContextAccessor: CreateAccessor("Bearer inbound-token"));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(new AuthorizationDecision(true, "allow", "allowed"), decision);
    }

    [Fact]
    public async Task AuthorizeAsync_PostsNormalizedCamelCaseRequestWithoutDomainAndWithDedicatedBearer()
    {
        string? body = null;
        using var handler = new StubHttpMessageHandler(async (request, cancellationToken) =>
        {
            Assert.Equal(HttpMethod.Post, request.Method);
            Assert.Equal(
                "https://rbac.example/api/v1/authorize",
                request.RequestUri?.AbsoluteUri);
            Assert.Equal("Bearer service-secret", request.Headers.Authorization?.ToString());
            Assert.Equal("application/json", request.Content?.Headers.ContentType?.MediaType);
            body = await request.Content!.ReadAsStringAsync(cancellationToken);
            return JsonResponse(AllowedJson);
        });
        var factory = new StubHttpClientFactory(handler);
        var provider = CreateProvider(factory);

        var decision = await provider.AuthorizeAsync(
            " user-1 ",
            "GET",
            "/orders/42");

        Assert.Equal(HttpAuthorizationProvider.HttpClientName, factory.RequestedClientName);
        Assert.Equal(new AuthorizationDecision(true, "allow", "allowed"), decision);
        Assert.NotNull(body);

        using var document = JsonDocument.Parse(body);
        var root = document.RootElement;
        Assert.Equal(5, root.EnumerateObject().Count());
        Assert.Equal("books", root.GetProperty("appKey").GetString());
        Assert.Equal("book-service", root.GetProperty("serviceResource").GetString());
        Assert.Equal("user-1", root.GetProperty("subject").GetString());
        Assert.Equal("GET", root.GetProperty("method").GetString());
        Assert.Equal("/orders/42", root.GetProperty("path").GetString());
        Assert.DoesNotContain(root.EnumerateObject(), property =>
            string.Equals(property.Name, "domain", StringComparison.OrdinalIgnoreCase));
    }

    [Fact]
    public async Task AuthorizeAsync_ReturnsLegalServerDenyReason()
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            Task.FromResult(JsonResponse(
                """{"allowed":false,"effect":"deny","reason":"endpoint_not_granted"}""")));
        var provider = CreateProvider(new StubHttpClientFactory(handler));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "endpoint_not_granted"),
            decision);
    }

    [Theory]
    [InlineData("not-json")]
    [InlineData("null")]
    [InlineData("{}")]
    [InlineData("""{"effect":"deny","reason":"endpoint_not_granted"}""")]
    [InlineData("""{"allowed":false,"reason":"endpoint_not_granted"}""")]
    [InlineData("""{"allowed":false,"effect":"deny"}""")]
    [InlineData("""{"Allowed":false,"effect":"deny","reason":"endpoint_not_granted"}""")]
    [InlineData("""{"allowed":false,"Effect":"deny","reason":"endpoint_not_granted"}""")]
    [InlineData("""{"allowed":false,"effect":"DENY","reason":"endpoint_not_granted"}""")]
    [InlineData("""{"allowed":true,"effect":"Allow","reason":"allowed"}""")]
    [InlineData("""{"allowed":true,"effect":"allow","reason":"ALLOWED"}""")]
    [InlineData("""{"allowed":true,"effect":"deny","reason":"endpoint_not_granted"}""")]
    [InlineData("""{"allowed":false,"effect":"allow","reason":"allowed"}""")]
    [InlineData("""{"allowed":false,"effect":"deny","reason":"allowed"}""")]
    [InlineData("""{"allowed":"true","effect":"allow","reason":"allowed"}""")]
    [InlineData("""{"allowed":true,"effect":true,"reason":"allowed"}""")]
    [InlineData("""{"allowed":true,"effect":"allow","reason":true}""")]
    [InlineData("""{"allowed":true,"effect":"allow","reason":"allowed","allowed":true}""")]
    public async Task AuthorizeAsync_RejectsMalformedOrInconsistentDecision(
        string responseBody)
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            Task.FromResult(JsonResponse(responseBody)));
        var provider = CreateProvider(new StubHttpClientFactory(handler));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "invalid_remote_response"),
            decision);
    }

    [Theory]
    [InlineData(HttpStatusCode.BadRequest)]
    [InlineData(HttpStatusCode.Unauthorized)]
    [InlineData(HttpStatusCode.Created)]
    [InlineData(HttpStatusCode.NoContent)]
    [InlineData(HttpStatusCode.InternalServerError)]
    [InlineData(HttpStatusCode.Redirect)]
    public async Task AuthorizeAsync_RejectsStatusesOtherThanOk(HttpStatusCode statusCode)
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            Task.FromResult(new HttpResponseMessage(statusCode)
            {
                Content = new StringContent(AllowedJson)
            }));
        var provider = CreateProvider(new StubHttpClientFactory(handler));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "remote_unavailable"),
            decision);
    }

    [Fact]
    public async Task AuthorizeAsync_MapsTransportFailureToRemoteUnavailable()
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            Task.FromException<HttpResponseMessage>(
                new HttpRequestException("transport failed")));
        var provider = CreateProvider(new StubHttpClientFactory(handler));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "remote_unavailable"),
            decision);
    }

    [Fact]
    public async Task AuthorizeAsync_MapsResponseStreamFailureToInvalidResponse()
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            Task.FromResult(new HttpResponseMessage(HttpStatusCode.OK)
            {
                Content = new StreamContent(new ThrowingReadStream())
            }));
        var provider = CreateProvider(new StubHttpClientFactory(handler));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "invalid_remote_response"),
            decision);
    }

    [Fact]
    public async Task AuthorizeAsync_TimesOutUsingConfiguredTimeout()
    {
        using var handler = new StubHttpMessageHandler(async (_, cancellationToken) =>
        {
            await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            throw new InvalidOperationException("unreachable");
        });
        var provider = CreateProvider(
            new StubHttpClientFactory(handler),
            timeoutMilliseconds: 25);

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "remote_timeout"),
            decision);
    }

    [Fact]
    public async Task AuthorizeAsync_ReturnsCancelledBeforeCreatingClient()
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            throw new InvalidOperationException("handler should not run"));
        var factory = new StubHttpClientFactory(handler);
        var provider = CreateProvider(factory);
        using var cancellation = new CancellationTokenSource();
        cancellation.Cancel();

        var decision = await provider.AuthorizeAsync(
            "user-1",
            "GET",
            "/orders",
            cancellation.Token);

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "request_cancelled"),
            decision);
        Assert.Null(factory.RequestedClientName);
    }

    [Fact]
    public async Task AuthorizeAsync_ReturnsCancelledWhenActiveRequestIsCancelled()
    {
        using var handler = new StubHttpMessageHandler(async (_, cancellationToken) =>
        {
            await Task.Delay(Timeout.InfiniteTimeSpan, cancellationToken);
            throw new InvalidOperationException("unreachable");
        });
        var provider = CreateProvider(
            new StubHttpClientFactory(handler),
            timeoutMilliseconds: 5000);
        using var cancellation = new CancellationTokenSource();
        cancellation.CancelAfter(25);

        var decision = await provider.AuthorizeAsync(
            "user-1",
            "GET",
            "/orders",
            cancellation.Token);

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "request_cancelled"),
            decision);
    }

    [Fact]
    public async Task AuthorizeAsync_RejectsMissingSubjectWithoutCallingRemote()
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            throw new InvalidOperationException("handler should not run"));
        var factory = new StubHttpClientFactory(handler);
        var provider = CreateProvider(factory);

        var decision = await provider.AuthorizeAsync("   ", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "subject_required"),
            decision);
        Assert.Null(factory.RequestedClientName);
    }

    [Theory]
    [InlineData("", "/orders")]
    [InlineData("   ", "/orders")]
    [InlineData("GET", "")]
    [InlineData("GET", "   ")]
    public async Task AuthorizeAsync_RejectsEmptyMethodOrPathWithoutCallingRemote(
        string method,
        string path)
    {
        using var handler = new StubHttpMessageHandler((_, _) =>
            throw new InvalidOperationException("handler should not run"));
        var factory = new StubHttpClientFactory(handler);
        var provider = CreateProvider(factory);

        var decision = await provider.AuthorizeAsync("user-1", method, path);

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "invalid_authorization_request"),
            decision);
        Assert.Null(factory.RequestedClientName);
    }

    [Fact]
    public async Task AuthorizeAsync_RejectsChunkedResponseLargerThanOneMiB()
    {
        const int maxResponseBytes = 1024 * 1024;
        var responseBody = new byte[maxResponseBytes + 1];
        Array.Fill(responseBody, (byte)' ');
        using var handler = new StubHttpMessageHandler((_, _) =>
            Task.FromResult(StreamResponse(responseBody)));
        var provider = CreateProvider(new StubHttpClientFactory(handler));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(
            new AuthorizationDecision(false, "deny", "invalid_remote_response"),
            decision);
    }

    [Fact]
    public async Task AuthorizeAsync_AcceptsResponseAtOneMiBLimit()
    {
        const int maxResponseBytes = 1024 * 1024;
        var allowedBytes = Encoding.UTF8.GetBytes(AllowedJson);
        var responseBody = new byte[maxResponseBytes];
        Array.Fill(responseBody, (byte)' ');
        allowedBytes.CopyTo(responseBody, 0);
        using var handler = new StubHttpMessageHandler((_, _) =>
            Task.FromResult(StreamResponse(responseBody)));
        var provider = CreateProvider(new StubHttpClientFactory(handler));

        var decision = await provider.AuthorizeAsync("user-1", "GET", "/orders");

        Assert.Equal(new AuthorizationDecision(true, "allow", "allowed"), decision);
    }

    private static HttpAuthorizationProvider CreateProvider(
        StubHttpClientFactory factory,
        int timeoutMilliseconds = 3000,
        string? serviceCredential = "service-secret",
        IHttpContextAccessor? httpContextAccessor = null)
    {
        var options = new NexusAuthorizationOptions
        {
            ServiceResource = " book-service "
        };
        var httpOptions = new HttpAuthorizationOptions
        {
            BaseUrl = "https://rbac.example///",
            AppKey = " books ",
            ServiceCredential = serviceCredential!,
            TimeoutMilliseconds = timeoutMilliseconds
        };

        return new HttpAuthorizationProvider(
            factory,
            Options.Create(options),
            Options.Create(httpOptions),
            NullLogger<HttpAuthorizationProvider>.Instance,
            httpContextAccessor ?? new HttpContextAccessor());
    }

    private static HttpContextAccessor CreateAccessor(params string[] authorizationHeaders)
    {
        var accessor = new HttpContextAccessor
        {
            HttpContext = new DefaultHttpContext()
        };
        if (authorizationHeaders.Length > 0)
        {
            accessor.HttpContext.Request.Headers["Authorization"] =
                new StringValues(authorizationHeaders);
        }

        return accessor;
    }

    private static HttpResponseMessage JsonResponse(string json) =>
        new(HttpStatusCode.OK)
        {
            Content = new StringContent(json, Encoding.UTF8, "application/json")
        };

    private static HttpResponseMessage StreamResponse(byte[] bytes)
    {
        var content = new StreamContent(new NonSeekableReadStream(bytes));
        return new HttpResponseMessage(HttpStatusCode.OK)
        {
            Content = content
        };
    }

    private sealed class StubHttpClientFactory(HttpMessageHandler handler)
        : IHttpClientFactory
    {
        public string? RequestedClientName { get; private set; }

        public HttpClient CreateClient(string name)
        {
            RequestedClientName = name;
            return new HttpClient(handler, disposeHandler: false);
        }
    }

    private sealed class StubHttpMessageHandler(
        Func<HttpRequestMessage, CancellationToken, Task<HttpResponseMessage>> send)
        : HttpMessageHandler
    {
        protected override Task<HttpResponseMessage> SendAsync(
            HttpRequestMessage request,
            CancellationToken cancellationToken) =>
            send(request, cancellationToken);
    }

    private sealed class NonSeekableReadStream(byte[] bytes) : Stream
    {
        private readonly MemoryStream _inner = new(bytes, writable: false);

        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => false;
        public override long Length => throw new NotSupportedException();
        public override long Position
        {
            get => throw new NotSupportedException();
            set => throw new NotSupportedException();
        }

        public override int Read(byte[] buffer, int offset, int count) =>
            _inner.Read(buffer, offset, count);

        public override int Read(Span<byte> buffer) =>
            _inner.Read(buffer);

        public override ValueTask<int> ReadAsync(
            Memory<byte> buffer,
            CancellationToken cancellationToken = default) =>
            _inner.ReadAsync(buffer, cancellationToken);

        public override void Flush()
        {
        }

        public override long Seek(long offset, SeekOrigin origin) =>
            throw new NotSupportedException();

        public override void SetLength(long value) =>
            throw new NotSupportedException();

        public override void Write(byte[] buffer, int offset, int count) =>
            throw new NotSupportedException();

        protected override void Dispose(bool disposing)
        {
            if (disposing)
            {
                _inner.Dispose();
            }

            base.Dispose(disposing);
        }
    }

    private sealed class ThrowingReadStream : Stream
    {
        public override bool CanRead => true;
        public override bool CanSeek => false;
        public override bool CanWrite => false;
        public override long Length => throw new NotSupportedException();
        public override long Position
        {
            get => throw new NotSupportedException();
            set => throw new NotSupportedException();
        }

        public override int Read(byte[] buffer, int offset, int count) =>
            throw new IOException("read failed");

        public override ValueTask<int> ReadAsync(
            Memory<byte> buffer,
            CancellationToken cancellationToken = default) =>
            ValueTask.FromException<int>(new IOException("read failed"));

        public override void Flush()
        {
        }

        public override long Seek(long offset, SeekOrigin origin) =>
            throw new NotSupportedException();

        public override void SetLength(long value) =>
            throw new NotSupportedException();

        public override void Write(byte[] buffer, int offset, int count) =>
            throw new NotSupportedException();
    }
}
