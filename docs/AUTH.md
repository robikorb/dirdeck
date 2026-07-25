# Authentication and CSRF (developers)

Phase 0 established single-user auth. All filesystem and transfer APIs require
a valid session except health/ready.

## Bootstrap

1. Operator supplies username and password via secret files.
2. On first startup, `BootstrapAdmin` hashes the password with **Argon2id** and
   creates the single `users` row (`id = 1`).
3. Only the Argon2id hash is stored in SQLite — never the plaintext password.
4. An ordinary restart with unchanged secret files keeps the existing hash and
   sessions. A changed username or password updates the hash and revokes every
   existing session.

## Sessions

- Opaque random session ID in an HTTP-only cookie: `lgfm_session`; SQLite stores
  only its SHA-256 digest
- `SameSite=Strict`; `Secure` when `DIRDECK_SECURE_COOKIE=true`
- CSRF token stored server-side with the session and returned to the client
  after login / session check
- Session rotation: previous cookie session is revoked on successful login
- Expired and revoked sessions are pruned at startup
- Default TTL: 12 hours (`DIRDECK_SESSION_TTL_HOURS`)

## Endpoints

| Method | Path | Auth | CSRF | Notes |
|--------|------|------|------|-------|
| `POST` | `/api/auth/login` | No | No | Rate-limited on failure |
| `POST` | `/api/auth/logout` | Yes | Yes | Revokes session |
| `GET` | `/api/auth/session` | Cookie optional | No | `{ authenticated, username?, csrfToken?, expiresAt? }` |

Failed logins are rate-limited per client IP. Defaults are 10 failures per 60
seconds and can be adjusted with `DIRDECK_LOGIN_RATE_LIMIT_MAX` and
`DIRDECK_LOGIN_RATE_LIMIT_SEC`.

## CSRF rules

State-changing requests (`POST`, `PUT`, `PATCH`, `DELETE`) must:

1. Carry a valid session cookie
2. Send header `X-CSRF-Token` matching the session’s token
3. Pass same-origin checks via `Origin` or `Referer` matching `Host`

The frontend stores the CSRF token from login/session and attaches it on
mutating `fetch` calls (`credentials: 'same-origin'`).

## What must not happen

- No filesystem metadata before authentication
- No credentials or session tokens in URLs
- No logging of passwords, session IDs, CSRF tokens, absolute host paths, or
  file contents
