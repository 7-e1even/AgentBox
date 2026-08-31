package store

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	encryptionKeySentinel       = "agentbox-encryption-key-sentinel/v1"
	encryptionKeyAdvisoryLockID = int64(0x4147424f584b4559) // "AGBOXKEY"
)

// verifyEncryptionKey binds the database to the configured credential key.
// Existing installations without a sentinel are adopted only after every
// existing ciphertext can be decrypted by that key.
func (s *Store) verifyEncryptionKey(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin encryption key verification: %w", err)
	}
	defer tx.Rollback(ctx)

	// The advisory lock serializes current AgentBox instances. The table lock
	// also closes the gap with an older instance that does not know about the
	// sentinel yet but may still be writing encrypted values during adoption.
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", encryptionKeyAdvisoryLockID); err != nil {
		return fmt.Errorf("lock encryption key verification: %w", err)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE encryption_key_metadata IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return fmt.Errorf("lock encryption key metadata: %w", err)
	}

	var ciphertext, nonce []byte
	err = tx.QueryRow(ctx, `SELECT sentinel_ciphertext, sentinel_nonce
		FROM encryption_key_metadata WHERE id = 'primary'`).Scan(&ciphertext, &nonce)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx, `LOCK TABLE provider_credentials, network_proxies,
			automations, control_resources IN SHARE MODE`); err != nil {
			return fmt.Errorf("lock existing encrypted data: %w", err)
		}
		if err := s.validateExistingEncryptedData(ctx, tx); err != nil {
			return fmt.Errorf("credential encryption key does not match existing data; restore the original AGENTBOX_SECRET_KEY or key file: %w", err)
		}
		ciphertext, nonce, err = encryptSecret(s.secretKey, encryptionKeySentinel)
		if err != nil {
			return fmt.Errorf("create encryption key sentinel: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO encryption_key_metadata
			(id, sentinel_ciphertext, sentinel_nonce) VALUES ('primary', $1, $2)
			ON CONFLICT (id) DO NOTHING`, ciphertext, nonce); err != nil {
			return fmt.Errorf("save encryption key sentinel: %w", err)
		}
		// Read back the authoritative row so concurrent first starts with
		// different generated keys cannot both become healthy.
		err = tx.QueryRow(ctx, `SELECT sentinel_ciphertext, sentinel_nonce
			FROM encryption_key_metadata WHERE id = 'primary'`).Scan(&ciphertext, &nonce)
	}
	if err != nil {
		return fmt.Errorf("load encryption key sentinel: %w", err)
	}
	plaintext, err := decryptSecret(s.secretKey, ciphertext, nonce)
	if err != nil || subtle.ConstantTimeCompare([]byte(plaintext), []byte(encryptionKeySentinel)) != 1 {
		return errors.New("credential encryption key does not match this database; restore the original AGENTBOX_SECRET_KEY or key file")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit encryption key verification: %w", err)
	}
	return nil
}

func (s *Store) validateExistingEncryptedData(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `
		SELECT 'provider credential', secret_ciphertext, secret_nonce FROM provider_credentials
		UNION ALL
		SELECT 'network proxy', password_ciphertext, password_nonce FROM network_proxies
		UNION ALL
		SELECT 'automation webhook', secret_ciphertext, secret_nonce FROM automations`)
	if err != nil {
		return fmt.Errorf("load existing encrypted secrets: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var ciphertext, nonce []byte
		if err := rows.Scan(&kind, &ciphertext, &nonce); err != nil {
			return fmt.Errorf("scan existing encrypted secret: %w", err)
		}
		if _, err := decryptSecret(s.secretKey, ciphertext, nonce); err != nil {
			return fmt.Errorf("decrypt existing %s: %w", kind, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read existing encrypted secrets: %w", err)
	}

	specRows, err := tx.Query(ctx, `SELECT id, spec FROM control_resources
		WHERE kind IN ('runtime', 'sandbox') AND spec::text LIKE '%enc:%'`)
	if err != nil {
		return fmt.Errorf("load encrypted resource environments: %w", err)
	}
	defer specRows.Close()
	for specRows.Next() {
		var id string
		var encoded []byte
		if err := specRows.Scan(&id, &encoded); err != nil {
			return fmt.Errorf("scan encrypted resource environment: %w", err)
		}
		var spec map[string]any
		if err := json.Unmarshal(encoded, &spec); err != nil {
			return fmt.Errorf("decode encrypted environment for resource %s: %w", id, err)
		}
		variables, _ := spec["environmentVariables"].([]any)
		for _, variable := range variables {
			entry, _ := variable.(map[string]any)
			value, _ := entry["value"].(string)
			if !strings.HasPrefix(value, encryptedEnvironmentPrefix) {
				continue
			}
			if _, err := decryptEnvironmentValue(s.secretKey, value); err != nil {
				return fmt.Errorf("decrypt environment for resource %s: %w", id, err)
			}
		}
	}
	if err := specRows.Err(); err != nil {
		return fmt.Errorf("read encrypted resource environments: %w", err)
	}
	return nil
}
