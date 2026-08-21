-- +migrate Up
CREATE TABLE grok_session_credentials (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    source VARCHAR(20) NOT NULL CHECK (source IN ('build_fallback', 'console', 'web')),
    encrypted_sso TEXT,
    encrypted_sso_rw TEXT,
    encrypted_cf_clearance TEXT,
    encrypted_browser_ua TEXT,
    bound_proxy_id BIGINT,
    status VARCHAR(20) NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'reauth_required')),
    last_error_code VARCHAR(50),
    last_error_at TIMESTAMPTZ,
    web_tier VARCHAR(10) CHECK (web_tier IN ('basic', 'super', 'heavy')),
    web_terms_version TEXT,
    web_terms_accepted_at TIMESTAMPTZ,
    web_birth_date_set_at TIMESTAMPTZ,
    web_nsfw_enabled_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_grok_session_credentials_status ON grok_session_credentials(status);
CREATE INDEX idx_grok_session_credentials_bound_proxy ON grok_session_credentials(bound_proxy_id);

-- +migrate Down
DROP TABLE IF EXISTS grok_session_credentials;
