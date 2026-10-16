# Permission Center E2E tests

These Playwright specs cover the current application permission workbench
(`dashboard/src/pages/policy-workbench`): application selection, service
resources, API endpoints, menus, roles, user bindings and permission simulation.
The retired NexusAuth service-resource gate, PDP publish, draft/snapshot and
release pages no longer exist, so no spec asserts them.

Install the Playwright browser once before the first run:

```bash
yarn playwright install chromium
```

Run the whole suite from the Dashboard package, where `playwright.config.ts`
defines `testDir: ../src/test/dashboard/e2e` and starts the Vite server on port `5275`:

```bash
cd dashboard && yarn test:e2e
```

The standard setup uses Playwright Chromium. Run `yarn playwright install chromium`
once after dependencies are installed. A local browser can be used for verification
without changing the project configuration:

```bash
PLAYWRIGHT_EXECUTABLE_PATH="/path/to/browser" npx playwright test
```

## Specs

- `nexus-authorization.spec.ts` - workbench key operations: application
  selection/switching and `app_key` request isolation, role create/edit/enable/
  delete, `subject-roles` assignment, permission simulation, the deleted release
  entry, plus service resource and API endpoint/Swagger import round-trips.
- `application-selector.spec.ts` - `/select-application`; a disabled application
  cannot be entered but stays editable and can be re-enabled.
- `service-form-values.spec.ts` / `api-form-values.spec.ts` - local service and
  API edit drawers echo stored values and submit them unchanged.
- `menu-workbench.spec.ts` - menu/button management tree, filters, parent picker
  and delete rules.
- `role-editor.spec.ts` - role authorization editor (menu/API grants, review
  drawer, dirty-draft blocking, save failure handling).
- `scim-user-binding.spec.ts` - assigning users to a role through the SCIM
  directory and `subject-roles`, including other-role/other-domain preservation.
- `simulate-form-values.spec.ts` - permission simulator defaults and payload.
- `workbench-list-layout.spec.ts` - list page layout contract.
- `workbench-navigation.spec.ts` - sidebar/breadcrumb/tabs/title driven by the
  async menu API module, hidden routes and the removed release entry.

## Mock boundary

The suite starts an isolated Vite server on port `5275` (`reuseExistingServer`
locally) and mocks the authenticated user plus every `/api/**` call from
in-memory fixtures through `page.route`. The selected business application lives
in `sessionStorage['nexus-authorization-application']` and scopes business calls
with `?app_key=`; role, endpoint, menu, service-resource, `subject-roles` and
simulator requests are asserted by URL and body. `workbench-navigation.spec.ts`
additionally swaps the `src/api/auth.ts` module to serve the menu list.

No spec touches NexusAuth, PostgreSQL, a real permission engine or a real SCIM
directory, and none of them exercise the production backend. They prove the UI
and HTTP contract only.
