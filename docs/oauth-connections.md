# Third-party OAuth2 connections

This document describes the per-user OAuth2 connection store added in this
PR, why it is shaped the way it is, and how it relates to the closed
issue [#184 "Implement OAuth callback URL"][issue-184].

## Goal

Let users connect a third-party account (Strava is the first concrete
provider) once, and have any pixlet app that needs that account render
with a fresh access token automatically. No more pasting refresh tokens
into the app config form.

The motivating example is Strava: today, the [`tronbyt/apps`
strava.star][tronbyt-strava] requires a user to register their own Strava
OAuth app, manually run an authorize URL, scrape `?code=` from the
resulting error page, and `curl` the token endpoint to exchange code for
refresh token, then paste three secrets into the app config form. After
this PR, the user clicks **Connect Strava** and authorizes in the
browser — the rest happens server-side.

## Why #184 was closed, and how this avoids the blocker

Issue #184 was closed `not_planned`. The maintainer concern was:

> Without a centralized system we couldn't set up, eg, a Google Calendar
> integration that you can OAuth to, because everyone would still have a
> separate callback URL.

That blocker is real for the Tidbyt-cloud model, where the *app
developer* bakes their `client_id` + encrypted `client_secret` into the
starlark, and Tidbyt-the-company runs `appauth.tidbyt.com` as a shared
redirect URI. A self-hosted Tronbyt instance has neither the secret-
decryption key nor the registered redirect URI, so that approach can't
work without Tronbyt-the-org running infrastructure.

This PR sidesteps the blocker by changing whose credentials are used:

- The **server admin** registers their own OAuth application with each
  provider (5-minute one-time setup at e.g.
  `https://www.strava.com/settings/api`).
- They set `STRAVA_CLIENT_ID` / `STRAVA_CLIENT_SECRET` (and the equivalent
  for any future provider) as env vars on the server.
- They register their callback URL — `https://<their-tronbyt>/oauth-callback`
  — with the provider. That URL is unique to their instance, so it is
  always valid for them.
- Every user on that instance shares the admin's OAuth app, exactly the
  way they share the server itself.

This means:
- **No Tronbyt-org infrastructure** required.
- **No pixlet changes** required (we honour the existing `schema.OAuth2`
  field shape).
- **No app-developer secret-decryption key** required.
- **Self-hoster cost**: ~5 minutes per provider, once. Zero per user.

## Architecture

### Data model

```go
type Connection struct {
    ID              uint
    UserID          string  // FK to User.Username
    Provider        string  // "strava", "spotify", ...
    ExternalID      string  // provider-side user id (e.g. Strava athlete.id)
    DisplayName     string  // shown as "Connected as <name>" in UI
    Scopes          string  // space-separated, granted scopes
    AccessToken     []byte  // AES-256-GCM encrypted
    RefreshToken    []byte  // AES-256-GCM encrypted
    AccessExpiresAt time.Time
    CreatedAt, UpdatedAt time.Time
}
// uniqueIndex(UserID, Provider): one connection per (user, provider).
```

Per-user, not per-app-instance. A user installing the Strava app on two
devices does not connect twice — both apps see the same connection.

### Encryption at rest

The session `secret_key` (already used to sign session cookies) is
HKDF-equivalent-derived (SHA-256 of a fixed info string + the key) into
a 32-byte AES-GCM key. Tokens are sealed as `nonce || ciphertext`.
Rotating the session secret invalidates all stored tokens, which is the
right behavior — a leaked session secret is a leaked token store.

### OAuth flow

```
   user                  Tronbyt server                provider
    │                          │                          │
    │  click "Connect Strava"  │                          │
    ├─────────────────────────►│                          │
    │                          │ store {state, return_to, │
    │                          │ provider} in session     │
    │   303 to provider        │                          │
    │◄─────────────────────────┤                          │
    │  authorize w/ scopes     │                          │
    ├──────────────────────────┴─────────────────────────►│
    │       303 to /oauth-callback?code=…&state=…         │
    │◄────────────────────────────────────────────────────┤
    │  GET /oauth-callback     │                          │
    ├─────────────────────────►│                          │
    │                          │ verify state, exchange   │
    │                          │ code for tokens          │
    │                          ├─────────────────────────►│
    │                          │   tokens                 │
    │                          │◄─────────────────────────┤
    │                          │ identify athlete, persist│
    │                          │ encrypted Connection row │
    │   303 to return_to       │                          │
    │◄─────────────────────────┤                          │
```

A single stable callback URL, `/oauth-callback`, handles every provider —
the in-flight provider name lives in the session, not the URL path. This
matches what each provider expects to be pre-registered.

### Render-time token injection

`RenderApp` calls `injectConnectionTokens` before invoking pixlet. The
helper:

1. Loads the app's oauth2 fields via `oauth2FieldsForApp` (cached — see
   below).
2. Maps each field's `authorization_endpoint` to a known provider via the
   registry.
3. Looks up the user's `Connection` for that provider.
4. Mints a fresh access token (refreshing if expired or about to be).
5. Sets `config[field.id] = "<access-token>"` — a plain string, since
   pixlet stringifies config values before they reach starlark.

Apps see a string bearer token; refresh tokens never leave the server.
Provider-side identifiers like Strava's athlete id are derivable from
the token itself.

This is deliberately *not* the Tidbyt contract, where the app's own
starlark `handler` exchanged a code and stored a refresh token. On a
self-hosted server an app cannot refresh anything: the refresh grant
needs the admin's `client_secret`, which must never reach starlark, and
`secret.decrypt` returns `None` without Tidbyt's key. Roughly a third of
the community `schema.OAuth2` apps already read `config[id]` as a bearer
token and work unmodified; the rest need a one-line app-side change.

#### Schema extraction is cached

Extracting oauth2 fields requires `renderer.GetSchema`, which loads and
executes the whole applet — the same work the render itself does, so a
naive call doubles per-render Starlark cost for *every* app, including
apps with no oauth2 fields. `oauth2FieldsForApp` caches the extracted
fields per `(path, mtime, size)` and revalidates with a single
`os.Stat`; negative results are cached too, so the common case is
essentially free. Measured on an M-series Mac: 2.8 ms → 0.9 µs for
`strava.star` (46 KB), 27.7 ms → 1.0 µs for `coingecko_price.star`
(870 KB). A Pi Zero 2 W is roughly 15–25× slower, so this is tens to
hundreds of milliseconds of CPU saved per render. App uploads rewrite
the `.star` file, so mtime/size invalidate the entry naturally;
directory-form apps skip the cache.

#### Schema handlers

`handleSchemaHandler` (which backs `schema.Generated` and
`schema.Typeahead`) injects tokens too. The canonical OAuth pattern is
`schema.Generated(source = <oauth2 field id>)`, whose handler expects
the token as its parameter — but the browser sends no value for an
oauth2 field, so the server substitutes the injected token when the
request's `source` names a field it just injected. Gating on the
injected set means a client-supplied `source` can only ever select a
token that same user could already read via config.

### Token freshness and refresh

`AccessTokenForUser` returns the stored access token unless it is
missing, expired, or within 60 seconds of expiring. A **zero expiry
means "never expires"**, following `x/oauth2`'s own convention —
providers that omit `expires_in` usually issue no refresh token either,
so treating zero as expired would refresh (and fail) on every render
forever.

Refreshes are **single-flighted per connection**. Strava and other
rotating-refresh-token providers invalidate the old refresh token the
moment a new one is issued, so two concurrent renders racing to refresh
can persist a dead token and brick the connection until the user
re-authorizes. The loser of the race re-reads the row under the lock and
returns the winner's fresh token.

### Providers

| Provider | Flow | Notes |
| --- | --- | --- |
| Strava | redirect (params) | Scopes must be **comma**-joined (`ScopeJoin`), not space-joined. Rotates refresh tokens. Athlete summary comes back in the token response. New apps are capped at 1 connected athlete ("Single Player Mode"), self-service upgrade to 10. The app's "Authorization Callback Domain" is the bare host, no scheme or port; `localhost`/`127.0.0.1` are always accepted. |
| Spotify | redirect (Basic header) | May omit `refresh_token` on refresh (keep the stored one). Redirect URIs must be HTTPS *except* literal loopback addresses — `http://127.0.0.1:8000/oauth-callback` works, but `localhost`, LAN IPs, and `.local` names over plain http are rejected. Development Mode allows 5 allowlisted users and requires the app owner to have Premium. |
| GitHub | **device** | Public client: client ID alone enables it, no secret and no redirect URI. Requires "Enable Device Flow" on the OAuth app (off by default; otherwise the flow fails with `device_flow_disabled`). Newly registered OAuth apps have **"Expire user access tokens" on by default** — 8-hour tokens plus a 6-month refresh token — so the refresh path matters here, and refreshing a device-flow token needs no secret either (see `refreshCreds`). GitHub answers a pending poll with HTTP **200** and an RFC error body, which the oauth2 library handles correctly. |

The redirect-flow providers are confidential clients: the token exchange
needs the admin's client secret, which is why the server brokers it.

## Device authorization grant (RFC 8628)

The redirect flow's weak spot on self-hosted hardware is the redirect URI
— every instance has a different address, and providers increasingly
refuse plain-http LAN hosts. The device grant sidesteps it entirely:
there is no redirect URI. The server asks the provider for a short code,
the user approves it on a phone, and the server polls for the token.

That fits a display particularly well, so the code is rendered **onto the
matrix itself** (`device_code_image.go`) as well as shown in the browser:
read it off the shelf, approve on your phone, done. The frame is pushed
under a fixed `__device_code.webp` ephemeral name — `GetNextAppImage`
serves ephemeral frames once and deletes them, and the fixed name means a
re-push replaces the pending code rather than queueing a backlog. The
frame is cleared when the flow ends. Font size steps down automatically
so a long code is never clipped.

Flows live in memory (`Server.deviceFlows`), keyed by a random id and
owned by the user who started them; the waiting page polls
`/connections/device/status/{id}`. Polling runs in a goroutine with a
deadline, so closing the browser tab doesn't abandon the connection.

### Library gotchas (golang.org/x/oauth2 v0.36.0)

Verified against the library source, since each one bites silently:

- `DeviceAccessToken` **blocks** until approval, denial, or expiry —
  always call it from a goroutine.
- **Always set `AuthStyle` explicitly.** With the zero value
  (auto-detect) and an empty secret, the library sends Basic auth with an
  empty password, and since it only caches the style on *success* — while
  every pending poll is an error — it probes both styles on every tick,
  doubling the request rate. `deviceConfig` forces `AuthStyleInParams`.
- If a provider omits `expires_in`, **no deadline is installed** and the
  loop polls forever; `pollDeviceFlow` supplies its own.
- The expiry error is often a `*url.Error` wrapping
  `context.DeadlineExceeded`, so compare with `errors.Is`, never `==`.
- Terminal errors arrive as `*oauth2.RetrieveError`; switch on
  `ErrorCode`. Microsoft spells denial `authorization_declined` rather
  than the RFC's `access_denied`.
- A provider that signals "pending" with a **bare HTTP status and no RFC
  error body** kills the loop after one poll.

### Adding Trakt (needs a hand-rolled poller)

Trakt is the obvious next device-flow provider for a media display, but
it cannot use `DeviceAccessToken` at all. Three independent
incompatibilities, verified against the live API:

1. Polling returns **bare status codes with a zero-length body** — 400 =
   pending, 429 = slow down, 410 = expired, 418 = denied, 404 = invalid,
   409 = already used. With no `error` field, the library's loop exits on
   the first poll.
2. The token request field is named **`code`**, not `device_code`, and
   Trakt sends no `grant_type` — a request-shape mismatch no response
   shim can fix.
3. Credentials must go in the body, so `AuthStyle` must be explicit.

So Trakt wants a small dedicated poller (~40 lines) rather than a
transport shim. Two further gotchas: the OAuth host is now
`https://auth.trakt.tv` (`api.trakt.tv` still works but is legacy), and
access tokens dropped to **24 hours** in March 2025 with single-use
refresh tokens — so it leans hard on the single-flighted refresh path.

### UI

`/connections` lists every provider the admin has configured along with
the user's connection state, with Connect/Disconnect controls. The nav
link appears only when at least one provider has credentials.

`web/templates/manager/configapp.html` gains a `case "oauth2"` in its
field renderer that produces one of three states based on the
server-annotated schema:

- **Provider unknown** — show a maintenance note ("add provider support
  in `internal/connections`").
- **Configured but not connected** — render a `Connect <Name>` link to
  `/connections/start/<provider>?return_to=…&scopes=…`.
- **Configured and connected** — show "Connected as <athlete name>" and
  a `Disconnect` POST form.

The original schema fields stay intact (apps still see their declared
`schema.OAuth2`); the annotation adds `tronbyt_*` keys the JS keys off.

## Adding a new provider

1. Add a `Provider{...}` constructor in `internal/connections/<name>.go`
   with the auth/token URLs, `AuthStyle`, any `ScopeJoin` quirk, and an
   optional `Identify` callback.
2. Register it in `NewServer` (where `Strava()` and `Spotify()` are).
3. Add `XYZ_CLIENT_ID` / `XYZ_CLIENT_SECRET` to `Settings` and route them
   through `(*Settings).ConnectionClientCreds`.
4. Update `.env.example`, including any provider-specific redirect-URI or
   user-cap rules the admin needs to know before registering their app.

A future provider that needs custom token-endpoint behaviour (e.g. a
non-standard refresh shape) can extend `Provider` with a
`RefreshFn`-style hook. None of the current providers need that.

### Not every integration should be OAuth

Several of the best ambient-display sources need no OAuth at all —
Goodreads shelf RSS, Last.fm, Todoist personal tokens, Open Library — and
a couple of the most-wanted OAuth providers are hostile to this model:
Google refresh tokens expire after 7 days while a Cloud project sits in
"Testing" status (admins must push their own project to production), and
Fitbit's API is being retired into Google's restricted-scope regime.
Providers offering the **device authorization grant** (GitHub, Trakt,
Microsoft, YouTube) are a better fit for a device on a shelf: no redirect
URI at all, and the code can be shown on the matrix itself. That would
slot in as a second grant type alongside the authorization-code flow.

## Migration

Pure additive: a single new `Connection` table created via GORM
`AutoMigrate`. No backfill, no data migration. The existing
`tronbyt/apps` Strava app continues to work with its three-secret-paste
flow until the companion PR over there switches it back to
`schema.OAuth2`.

## Threat model notes

- **Open-redirect**: `return_to` is rejected unless it starts with `/`,
  parses as a path-only URL, and contains no backslash. The backslash
  check matters: `url.Parse` treats `\` as an ordinary path byte, but
  browsers normalize `/\evil.com` into the protocol-relative
  `//evil.com`, which would otherwise slip past the `//` guard and leak
  the flow's `state` to the attacker origin via `Referer`.
- **CSRF on the OAuth flow**: a 32-byte random `state` is verified on
  callback against the session.
- **CSRF on disconnect**: relies on SameSite=Lax cookies (matches the
  rest of the app).
- **Token leakage**: refresh + access tokens encrypted at rest; never
  surfaced to apps; `json:"-"` on the model so they don't appear in any
  serialization.
- **Provider impersonation**: the callback handler never decodes user
  input as authoritative — the provider name comes from the session
  pending state, not the URL.

[issue-184]: https://github.com/tronbyt/server/issues/184
[tronbyt-strava]: https://github.com/tronbyt/apps/tree/main/apps/strava
