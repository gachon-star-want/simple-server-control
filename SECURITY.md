# Security Policy (`simple-server-control`)

## Core Security Guarantees

1. **Per-Project Network & Volume Isolation**
   - Each project receives its own Docker bridge network (`ssc-proj-<slug>`).
   - Managed database containers (PostgreSQL, Redis, MySQL) attach **only** to their project network with **zero** host port bindings and are never joined to `ssc-ingress`.
   - Application containers bind **zero** host ports; HTTP/HTTPS ingress enters exclusively through `ssc-caddy` on `ssc-ingress`.

2. **Secret Encryption & Zero-Leakage Canvas**
   - All environment variables, `.env` secrets, GitHub App private keys, and webhook/client secrets are encrypted at rest with **AES-256-GCM** using `/var/lib/ssc/master.key` (`0600`).
   - Canvas & overview payloads (`/api/projects/:id/canvas`, `/api/projects`) never return secret values.
   - Sealed secrets (`is_sealed = true`) are write-only and cannot be revealed via API queries after creation.

3. **Authentication & Webhook Verification**
   - First-boot admin onboarding requires the random `setup.token` generated in `/var/lib/ssc/setup.token`, permanently locking `/api/auth/setup` after completion.
   - Browser sessions use `HttpOnly; SameSite=Lax` signed JWT cookies backed by `jwt.secret` (`0600`).
   - GitHub App Manifest and installation callbacks enforce single-use CSRF `state` validation and server-to-server RS256 App JWT ownership verification against `GET /app/installations/{id}`.
   - GitHub webhooks verify `X-Hub-Signature-256` with constant-time `hmac.Equal`.

4. **Safe Build Secret Handling**
   - GitHub installation access tokens are supplied via temporary `0700` `GIT_ASKPASS` helpers deleted immediately after clone and are never embedded in command arguments or persisted to build logs.

## Reporting a Vulnerability

Please report security issues privately via GitHub Security Advisories (`https://github.com/gachon-star-want/simple-server-control/security/advisories/new`) rather than opening a public issue.
