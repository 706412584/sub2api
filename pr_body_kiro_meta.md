## Summary
- Adds the GHCR image build path so the low-memory Baota server only needs to pull a prebuilt image instead of running frontend/backend Docker builds locally.
- Completes Kiro account import/management plumbing that was only partially merged from `kiro-go-plus`:
  ```diff
  + platform IN (..., 'kiro')
  + AllowedQuotaPlatforms += PlatformKiro
  + NewKiroTokenRefresher() registered in TokenRefreshService
  + RefreshKiroAccountToken(): idc | social | external_idp refresh flows
  ```
- Expands Kiro JSON import compatibility to preserve credentials plus non-secret subscription/usage metadata from Kiro/Kiro-Go Plus exports:
  ```ts
  extra.kiro_subscription = { type, title, raw_type, days_remaining, expires_at, overage_capability }
  extra.kiro_usage = { current, limit, percent_used, base_limit, next_reset_date, overage_enabled, overage_cap, overage_rate }
  ```
- Shows Kiro subscription, quota usage, and overage-enabled status inline in the admin account table without exposing token fields.
- Adds Kiro to admin/user platform selectors, badges, quota tables, channel/group platform ordering, and error passthrough platform validation.
- Redacts additional credential secrets (`client_secret`, `external_idp`) from account API responses and adds tests for import parsing, external IdP refresh, quota SQL/platform allowlists, and credential redaction.
