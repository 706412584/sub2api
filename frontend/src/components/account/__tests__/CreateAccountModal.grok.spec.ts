import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

const source = readFileSync(
  resolve(process.cwd(), 'src/components/account/CreateAccountModal.vue'),
  'utf8'
)

describe('CreateAccountModal Grok account types', () => {
  it('offers API-key setup alongside OAuth with the official xAI default', () => {
    expect(source).toContain('data-testid="grok-account-type-api-key"')
    expect(source).toContain("@click=\"accountCategory = 'apikey'\"")
    expect(source).toContain("newPlatform === 'grok'")
    expect(source).toContain("? 'https://api.x.ai/v1'")
    expect(source).toContain("form.platform === 'grok'")
    expect(source).toContain(':placeholder="apiKeyValuePlaceholder"')
    expect(source).toContain("return 'xai-...'")
  })

  it('exposes custom upstream URL and header override for the OAuth create flow', () => {
    expect(source).toContain('data-testid="grok-custom-base-url-toggle"')
    expect(source).toContain('data-testid="grok-custom-base-url-input"')
    expect(source).toContain('form.platform === \'grok\' && isOAuthFlow')
  })

  it('validates and applies upstream config on Grok OAuth create paths', () => {
    // 授权码兑换 / RT 批量 / SSO 批量（密码授权已隐藏）
    expect(source.match(/validateGrokOAuthUpstreamConfig\(\)/g)?.length).toBeGreaterThanOrEqual(3)
    expect(source.match(/applyGrokOAuthUpstreamConfig\(credentials\)/g)?.length).toBeGreaterThanOrEqual(3)
  })

  it('hides Grok password authorize option in the create flow', () => {
    expect(source).toContain(':show-email-password-option="false"')
  })
})

describe('CreateAccountModal Grok Console/Web session entries', () => {
  it('offers Console and Web session account types', () => {
    expect(source).toContain('data-testid="grok-account-type-console"')
    expect(source).toContain("accountCategory = 'grok_console'")
    expect(source).toContain('data-testid="grok-account-type-web"')
    expect(source).toContain("accountCategory = 'grok_web'")
  })

  it('maps grok session categories to dedicated account types', () => {
    expect(source).toContain("form.platform === 'grok' && (category === 'grok_console' || category === 'grok_web')")
    expect(source).toContain('form.type = category')
  })

  it('requires proxy, UA and source-specific materials for session import', () => {
    // Console: SSO + UA + proxy；Web 额外要求 cf_clearance
    expect(source).toContain("admin.accounts.grokSession.ssoRequired")
    expect(source).toContain("admin.accounts.grokSession.cfRequired")
    expect(source).toContain("admin.accounts.grokSession.uaRequired")
    expect(source).toContain("admin.accounts.grokSession.proxyRequired")
    expect(source).toContain("data-testid=\"grok-session-proxy\"")
  })

  it('creates the account first, then imports the session via the dedicated API', () => {
    expect(source).toContain('handleCreateGrokSessionAccount')
    expect(source).toContain('adminAPI.grok.saveConsoleSession(createdAccountId, sessionPayload)')
    expect(source).toContain('adminAPI.grok.saveWebSession(createdAccountId, sessionPayload)')
    // 会话材料不进通用 credentials
    expect(source).toContain('credentials: { placeholder: true }')
  })

  it('clears session inputs after submit and cleans up on failure', () => {
    expect(source).toContain('resetGrokSessionForm()')
    expect(source).toMatch(/await adminAPI\.accounts\.delete\(createdAccountId\)/)
  })
})
