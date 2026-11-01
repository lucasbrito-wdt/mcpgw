-- Revoked authorization grant families. A family is revoked when an authorization code or refresh
-- token is replayed; access tokens carry the family id (fid) and are rejected while it is listed here.
-- Needed because a client without the refresh_token grant has no oauth_refresh_tokens rows to mark.
-- Rows only matter until the last access token of the family expires, so they are purged shortly after.
CREATE TABLE oauth_revoked_families (
    family_id  uuid PRIMARY KEY,
    revoked_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX oauth_revoked_families_revoked_at_idx ON oauth_revoked_families (revoked_at);
