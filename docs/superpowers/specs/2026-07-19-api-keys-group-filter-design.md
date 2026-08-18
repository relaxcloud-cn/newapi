# API Keys Group Filter Design

## Goal

Add a group filter to the `/keys` page that matches the existing `/channels`
group filter in appearance and behavior. Users can type to narrow the available
groups and select one group to filter the paginated API key list.

## User Experience

- Add a `Group` faceted filter to the `/keys` table toolbar beside the existing
  status filter.
- Reuse `DataTableFacetedFilter`, including its searchable command list,
  checkbox-style option indicator, selected badge, clear action, and responsive
  toolbar behavior.
- Match `/channels` by setting `singleSelect: true`. The control looks like a
  checkbox list but permits one selected group at a time.
- Include `All Groups` as the unfiltered option.
- Store the selected value in the route search state under `group`, using the
  same array serialization pattern as `/channels`, so refresh and navigation
  preserve the filter.
- Fetch options from the current-user groups endpoint because `/keys` is
  available to ordinary users, while the channel group endpoint is admin-only.
- Allow the group filter to combine with name, API key, and status filters.

## Data Flow

1. The `/keys` route validates `group` as an optional string array.
2. `ApiKeysTable` reads the selected group from table URL state.
3. The table fetches current-user group metadata and maps group names to filter
   options.
4. Selecting a group resets pagination through the existing table URL-state
   behavior and adds the selected group to the API request.
5. Both regular listing and text-search requests use the same server-side group
   predicate, so pagination totals and page contents remain correct.

## Backend Contract

- Add an optional `group` query parameter to both:
  - `GET /api/token/`
  - `GET /api/token/search`
- Apply an exact group match together with the authenticated user ID.
- Preserve the current behavior when `group` is empty or omitted.
- Keep name and key substring matching unchanged.
- Implement filtering with GORM so SQLite, MySQL, and PostgreSQL remain
  supported.

## Error And Loading Behavior

- A failure to load group options must not prevent the API key list from
  loading; the filter simply has no selectable group options.
- Existing list/search API error handling and loading indicators remain in use.
- An unknown group value may return an empty page; it must never broaden the
  query beyond the authenticated user's keys.

## Verification

- Backend tests verify group-only filtering, group combined with substring
  search, correct totals, and isolation between users.
- Frontend tests verify request parameter construction and URL-backed selected
  state where the existing test harness provides a stable behavior-level seam.
- Run focused Go tests, frontend type checking, linting for changed files, and a
  production frontend build.
- Start the local service with fixture keys in multiple groups and use the
  `computer-use` plugin to verify that typing narrows group options, selecting a
  group updates the list, combined search remains correct, and clearing restores
  all groups.
- Build a `linux/amd64` Docker image tagged `newapi:amd64`, inspect its platform,
  and run a container-level health/status check before delivery.

## Scope

This change does not add true multi-group OR filtering, alter API key creation
or editing, change channel filtering, or introduce a new selector component.
