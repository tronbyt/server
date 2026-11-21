# Server-managed OAuth setup (GitHub example)

Tronbyt can run the OAuth 2.0 authorization code flow with PKCE for Pixlet apps that declare a **server-managed** `schema.OAuth2` field (one that includes `token_endpoint`). You register an OAuth application with the provider, store the credentials in Tronbyt, then sign in on each app’s config page. Tokens are kept in the database, refreshed by the server, and passed to the app at render time—they are not stored in `App.Config` or device exports.

This guide walks through **[GitHub Assigned Issues](data/system-apps/apps/githubassignedissues/)** as a reference app. The same steps apply to other providers once you use their authorize and token URLs and register Tronbyt’s redirect URI with them.

## Requirements

- A Tronbyt Server build that includes OAuth clients and app sign-in (see [PR #965](https://github.com/tronbyt/server/pull/965) or your feature branch).
- **Pixlet** with server-managed OAuth schema support (e.g. pixlet **v0.55.1+**, which ships with current server dependencies).
- A GitHub account.
- **One stable Tronbyt URL** for setup and sign-in (scheme, host, and port). The redirect URI Tronbyt shows is built from that request (e.g. `http://m1pro.local:8000/oauth/callback/…` or `http://synol.local:8000/oauth/callback/…`). Register that exact string with the provider. LAN hostnames and `http://` are fine on Tronbyt’s side; **the provider** decides whether it accepts non-HTTPS callbacks (GitHub often allows `http://` on local/LAN URLs you register; many others require `https://`).
- **HTTPS** when a provider rejects `http://` (reverse proxy, Cloudflare Tunnel, Tailscale Serve/Funnel, ngrok with a stable domain, etc.). Open Tronbyt over HTTPS before copying the redirect URI so it starts with `https://`.

**Redirect URI vs authorization/token URLs:** The callback is whatever host you use to open the UI (not limited to `localhost`). The **authorization** and **token** endpoints you type in the OAuth client form must be `https://` for real providers (Tronbyt only allows `http://` on those fields for loopback test hosts). GitHub’s endpoints are always `https://github.com/...`.

## How the pieces fit together

```text
  Pixlet app schema          Tronbyt Settings              Provider (GitHub)
  ─────────────────          ───────────────              ─────────────────
  authorization_endpoint  →  OAuth client (same pair)  →  OAuth App registration
  token_endpoint               client ID + secret           redirect URI = Tronbyt callback
  scopes                       per-user or shared

  App config page  →  Sign in  →  provider login  →  GET /oauth/callback/{client}
                    →  token stored  →  render injects field ID (e.g. github_auth)
```

Tronbyt only offers an OAuth client for an app field when the **authorization** and **token** endpoints in Settings match those declared in the app schema (after normalization: host case, default ports, trailing slashes).

---

## 1. Install the app, then register Tronbyt and GitHub together

1. Install **GitHub Assigned Issues** on a device (system app or from [tronbyt-apps](https://github.com/tronbyt/tronbyt-apps/tree/main/apps/githubassignedissues)).
2. Open that app’s **config** page using the hostname you will keep for sign-in (e.g. `http://synol.local:8000`).

GitHub needs Tronbyt’s **redirect URI** before you register the OAuth app. Tronbyt needs GitHub’s **client ID** and **client secret** before you can save. The usual path is **Set up an OAuth client** on the app config page (name and GitHub endpoints are prefilled from the app schema; after **Save**, you return to the same config page). **Settings → OAuth Clients** works too if you enter the same endpoints manually.

| | URL |
|---|-----|
| Authorization | `https://github.com/login/oauth/authorize` |
| Token | `https://github.com/login/oauth/access_token` |

### Step A — Tronbyt: open the client form and copy the redirect URI

1. On the app config page, under the **GitHub** OAuth field, click **Set up an OAuth client** (or **Add another OAuth client** if you already have one for GitHub).
2. On **New OAuth Client**, confirm **Name** and the two endpoints match the table (they should already be filled).
3. Leave **Client ID** and **Client secret** empty for now.
4. **Copy** the read-only **Redirect URI** (e.g. `http://synol.local:8000/oauth/callback/a2f03cc62d665d90`). The id in that path is fixed while this form is open—**keep this browser tab open** until step C. Reloading the new-client form generates a different redirect URI; if you reload, copy again and update GitHub.

<details>
<summary>Alternative: start from Settings → OAuth Clients</summary>

Open **Settings → OAuth Clients**, use **New OAuth Client**, and type the name and GitHub endpoints from the table yourself. You will not get an automatic return to the app config after save unless you opened the form with a `return_to` link from the app.

</details>

### Step B — GitHub: create the OAuth app

Use a classic **[OAuth App](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app)**, not a **GitHub App** ([Developer settings → **OAuth Apps**](https://github.com/settings/developers) → **New OAuth App**).

1. **Application name** and **Homepage URL**: anything you like (e.g. your Tronbyt URL).
2. **Authorization callback URL**: paste the **Redirect URI** from step A exactly.
3. **Register application**. Copy GitHub’s **Client ID**.
4. **Generate a new client secret** and copy it once (GitHub may only show it at creation).

If you landed on **New GitHub App** instead: Tronbyt does not use webhooks for this flow—under **Webhook**, leave **Active** unchecked. Prefer an **OAuth App** (above) for this guide.

### Step C — Tronbyt: finish, save, sign in

Back in the **same** Tronbyt tab from step A:

1. Paste GitHub’s **Client ID** and **Client secret**.
2. **Shared** (admins only): enable if every user on the server may pick this client; each user still signs in with their own GitHub account.
3. **Save** the client. When you started from the app config, you are redirected back to that config page with the new client available.

On the app config page:

4. Select your OAuth client (if more than one match GitHub).
5. Click **Sign in**, approve at GitHub, and confirm the field shows connected. Use **Disconnect** to remove the token.

**Note:** Sign in is a full-page navigation. Save other config changes before signing in, or they will be lost.

Scopes (`read:user`, `repo` for private issues) come from the app schema at sign-in, not from the Tronbyt client form.

The app prefers OAuth over a personal access token: it reads `config.get("github_auth")` first, then falls back to the `access_token` text field. At render time, Tronbyt injects the access token into `github_auth` when connected.

---

## 2. Verify

- Preview or push the app: you should see assigned issue counts (or errors if scopes are too narrow).
- The config page should not show the OAuth token value (tokens are not part of saved config JSON).
- After access tokens expire, the server refreshes them when the app renders; if refresh fails, sign in again.

---

## 3. Add server-managed OAuth to your own app

Pattern used by GitHub Assigned Issues:

**Schema** — include `token_endpoint` and omit Tidbyt-style `handler` / embedded provider `client_id`:

```python
schema.OAuth2(
    id = "github_auth",
    name = "GitHub",
    desc = "Sign in on Tronbyt.",
    icon = "github",
    authorization_endpoint = "https://github.com/login/oauth/authorize",
    token_endpoint = "https://github.com/login/oauth/access_token",
    scopes = ["read:user", "repo"],
)
```

**Runtime** — use the same field `id` in `main()` (value is injected only when signed in):

```python
def main(config):
    token = config.get("github_auth")
    if not token:
        return render_error("Sign in with GitHub.", "#ffffff")
    # Call APIs with Authorization: Bearer ...
```

Optional `authorization_params` (e.g. `prompt = "consent"`) are sent on the authorize request unless they conflict with reserved OAuth parameters.

Catalog apps that only define Tidbyt’s original OAuth shape (no `token_endpoint`, with `oauth_handler`) still show a message that Tidbyt cloud sign-in is not supported on Tronbyt Server until they are updated to this form.

---

## Troubleshooting

| Symptom | Things to check |
|--------|------------------|
| No OAuth client listed on the app config page | Endpoints in Settings must match the app schema exactly. Create a client with the same authorization and token URLs. |
| GitHub redirects with an error | Callback URL in GitHub must match Tronbyt’s redirect URI character-for-character (scheme, host, path). |
| Redirect URI / callback mismatch | Tronbyt, GitHub, and the browser must use the same scheme, host, port, and path. Re-copy the URI from Settings while logged in on the correct host; update GitHub if you changed servers or OAuth client. |
| Provider rejects `http://` redirect (not GitHub) | Open Tronbyt over HTTPS and re-copy the redirect URI from Settings. |
| “Sign-in failed” after GitHub redirects back | Wrong client secret, clock skew, or expired authorization code (try again). Check server logs for token exchange errors. |
| “The sign-in link is invalid or was already used” | State mismatch, expired sign-in (10 minutes), or callback opened without the same browser session. Start sign-in again from the config page. |
| API errors with connected OAuth | Scopes on the client authorize step must cover what the app calls (`repo` for private issues). |
| Unsaved config disappeared | Expected: complete sign-in only after saving other fields, or sign in first then edit. |

---

## Other providers (e.g. SolarEdge)

Use the same flow: install the app → **Set up an OAuth client** on its config page → provider registration → save on Tronbyt → **Sign in** on the config page. The app’s `schema.OAuth2` must declare the same **authorization** and **token** endpoints as the Tronbyt client.

Provider-specific quirks (missing `state` on callback, token endpoint auth style, Site Access requiring a real site consent) are independent of GitHub; test sign-in on your server before porting a large app.

---

## Further reading

- [Pull request #965](https://github.com/tronbyt/server/pull/965) — design, deployment, and limitations.
- [GitHub Assigned Issues README](data/system-apps/apps/githubassignedissues/README.md) — app-specific auth notes.
- [GitHub: Creating an OAuth app](https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/creating-an-oauth-app).
