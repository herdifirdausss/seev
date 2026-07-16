-- 000021_auth: identity & credentials for the auth module (docs/plan/25 Task
-- T1, following the internal/auth outline locked in docs/plan/24).
--
-- auth_users is the IDENTITY record — the ledger's accounts.owner_id points
-- at auth_users.id from now on for newly registered users, but the ledger
-- itself never reads these tables (module boundary: only internal/auth
-- touches auth_*).

CREATE TABLE auth_users (
    id         UUID        PRIMARY KEY,
    email      TEXT        NOT NULL,
    full_name  TEXT        NOT NULL DEFAULT '',
    role       TEXT        NOT NULL DEFAULT 'user' CHECK (role IN ('user','admin')),
    status     TEXT        NOT NULL DEFAULT 'active' CHECK (status IN ('active','disabled')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Case-insensitive uniqueness: "A@x.com" and "a@x.com" are the same account.
CREATE UNIQUE INDEX idx_auth_users_email ON auth_users (lower(email));

-- Credentials live in their own table (not a column on auth_users) so a
-- profile read never accidentally SELECTs the hash, and a future
-- passwordless/OAuth identity simply has no row here.
CREATE TABLE auth_credentials (
    user_id       UUID        PRIMARY KEY REFERENCES auth_users(id),
    password_hash TEXT        NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Refresh tokens are OPAQUE random values; only the SHA-256 hex of the token
-- is stored — a DB leak alone can never be replayed as a live token.
-- Rotation chain: each /auth/refresh revokes the old row (revoked_at) and
-- records its successor (replaced_by). Reuse of a revoked token is treated
-- as replay and revokes the user's entire chain (docs/plan/25 T1 step 2).
CREATE TABLE auth_refresh_tokens (
    id          UUID        PRIMARY KEY,
    user_id     UUID        NOT NULL REFERENCES auth_users(id),
    token_hash  TEXT        NOT NULL UNIQUE,
    expires_at  TIMESTAMPTZ NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at  TIMESTAMPTZ NULL,
    replaced_by UUID        NULL
);

CREATE INDEX idx_auth_refresh_user ON auth_refresh_tokens (user_id, expires_at);

-- Grants + RLS, same pattern as 000019/000020: the app connects as
-- app_service (no DDL), reporting reads as app_readonly. No DELETE anywhere —
-- users are disabled (status), never erased; tokens are revoked, never
-- deleted (audit trail).
GRANT SELECT, INSERT, UPDATE ON auth_users, auth_credentials, auth_refresh_tokens TO app_service;
GRANT SELECT ON auth_users TO app_readonly;

ALTER TABLE auth_users          ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_users          FORCE ROW LEVEL SECURITY;
ALTER TABLE auth_credentials    ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_credentials    FORCE ROW LEVEL SECURITY;
ALTER TABLE auth_refresh_tokens ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_refresh_tokens FORCE ROW LEVEL SECURITY;

CREATE POLICY pol_all_service   ON auth_users          FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_read_readonly ON auth_users          FOR SELECT TO app_readonly USING (true);
CREATE POLICY pol_all_service   ON auth_credentials    FOR ALL    TO app_service  USING (true) WITH CHECK (true);
CREATE POLICY pol_all_service   ON auth_refresh_tokens FOR ALL    TO app_service  USING (true) WITH CHECK (true);
