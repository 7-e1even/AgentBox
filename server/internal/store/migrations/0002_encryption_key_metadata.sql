CREATE TABLE encryption_key_metadata (
  id TEXT PRIMARY KEY CHECK (id = 'primary'),
  sentinel_ciphertext BYTEA NOT NULL,
  sentinel_nonce BYTEA NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
