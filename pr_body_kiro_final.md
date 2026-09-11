## Summary
- Adds the GHCR image build path so the low-memory Baota server only needs to pull a prebuilt image instead of running frontend/backend Docker builds locally.
- Completes Kiro account import/management plumbing that was only partially merged from `kiro-go-plus`:
  ```diff
  + platform IN (..., 'kiro')
  + AllowedQuotaPlatforms += PlatformKiro
  + NewKiroTokenRefresher() registered in TokenRefreshService
  + RefreshKiroAccountToken(): idc | social | external_idp refresh flows
  ```
- Expands Kiro import compatibility for both direct single-account exports and wrappers like `{ "account": ... }` / `{ "accounts": [...] }`, preserving fixed-key credentials plus non-secret subscription/usage metadata:
  ```ts
  extra.kiro_subscription = { type, title, raw_type, days_remaining, expires_at, overage_capability }
  extra.kiro_usage = { current, limit, percent_used, base_limit, next_reset_date, overage_enabled, overage_cap, overage_rate }
  credentials.machine_id // retained for native Kiro request fingerprinting
  ```
- Shows Kiro subscription, quota usage, and overage-enabled status inline in the admin account table, with subscription-tier colors for Free / Pro / Power / Pro+ / Pro Max.
- Fixes admin Kiro account testing so it uses Kiro's native request shape instead of probing Anthropic with a Kiro token, preserves the selected Sonnet 4.5/4.6 model instead of coercing all Sonnet tests to 4.6, and retries once with a freshly refreshed Kiro token when upstream returns 401/403 auth errors:
  ```http
  POST https://q.{region}.amazonaws.com/generateAssistantResponse
  Authorization: Bearer <kiro access token>
  x-amzn-kiro-agent-mode: vibe
  x-amzn-codewhisperer-optout: true
  Amz-Sdk-Invocation-Id: <uuid>
  ```
- Clears Kiro error status after a successful token refresh so non-banned accounts return to the normal status label.
- Documents low-memory production update rules in `deploy/README.md`: no server-side builds, no `.env` overwrite, no database storage replacement, and only pull when switching to a new image SHA/tag or when the image is missing locally.
- Adds Kiro to admin/user platform selectors, badges, quota tables, channel/group platform ordering, and error passthrough platform validation.
- Redacts additional credential secrets (`client_secret`, `external_idp`) from account API responses and adds tests for import parsing, native Kiro test request headers, auth-failure refresh retry, selected-model preservation, external IdP refresh, quota SQL/platform allowlists, and credential redaction.
