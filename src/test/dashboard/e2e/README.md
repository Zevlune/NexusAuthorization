# Permission Center E2E tests

The Playwright suite starts an isolated Vite server on port `5275`. It mocks
the authenticated user and permission-center API routes, so it does not need
NexusAuth, PostgreSQL, or a running backend.

Install the Playwright browser once before the first run:

```bash
yarn playwright install chromium
```

Run the suite from this directory with:

```bash
yarn test:e2e
```

The standard setup uses Playwright Chromium. Run `yarn playwright install chromium`
once after dependencies are installed. A local browser can be used for verification
without changing the project configuration:

```bash
PLAYWRIGHT_EXECUTABLE_PATH="/path/to/browser" yarn test:e2e
```

The tests seed `permission-center-service-resource` when a selected service
resource is needed and assert the service-resource gate, Header context, role
creation flow, and user-role refresh flow.
