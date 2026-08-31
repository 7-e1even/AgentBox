package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"agentbox/internal/catalog"
)

func TestStoreRejectsEncryptionKeyThatDoesNotMatchDatabase(t *testing.T) {
	store := newIntegrationTestStore(t)
	t.Setenv("AGENTBOX_SECRET_KEY", base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)))

	other, err := New(t.Context(), store.pool.Config().ConnString(), catalog.Catalog{})
	if other != nil {
		other.Close()
	}
	if err == nil {
		t.Fatal("opening the database with a different encryption key succeeded")
	}
	if !strings.Contains(err.Error(), "does not match this database") {
		t.Fatalf("open error = %q, want encryption key mismatch", err)
	}
}

func TestEncryptionKeyAdoptionWaitsForLegacyEncryptedWrite(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	if err := store.migrate(t.Context()); err != nil {
		t.Fatalf("migrate isolated store: %v", err)
	}

	legacyKey := bytes.Repeat([]byte{3}, 32)
	candidateKey := bytes.Repeat([]byte{4}, 32)
	ciphertext, nonce, err := encryptSecret(legacyKey, "legacy-secret")
	if err != nil {
		t.Fatalf("encrypt legacy secret: %v", err)
	}
	writer, err := store.pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin legacy writer: %v", err)
	}
	defer writer.Rollback(context.Background())
	if _, err := writer.Exec(t.Context(), `INSERT INTO provider_credentials
		(id, name, provider_id, secret_ciphertext, secret_nonce, created_at, updated_at)
		VALUES ('legacy-credential', 'Legacy credential', 'openai', $1, $2, NOW(), NOW())`,
		ciphertext, nonce); err != nil {
		t.Fatalf("stage legacy encrypted write: %v", err)
	}

	candidate := &Store{pool: store.pool, secretKey: candidateKey}
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		finished <- candidate.verifyEncryptionKey(t.Context())
	}()
	<-started
	lockPoll := time.NewTicker(20 * time.Millisecond)
	defer lockPoll.Stop()
	lockDeadline := time.NewTimer(5 * time.Second)
	defer lockDeadline.Stop()
waitForTableLock:
	for {
		select {
		case err := <-finished:
			t.Fatalf("key adoption completed while a legacy encrypted write was open: %v", err)
		case <-lockPoll.C:
			var waiting bool
			if err := store.pool.QueryRow(t.Context(), `SELECT EXISTS(
				SELECT 1 FROM pg_locks
				WHERE relation = 'provider_credentials'::regclass AND NOT granted
			)`).Scan(&waiting); err != nil {
				t.Fatalf("observe encryption key adoption lock: %v", err)
			}
			if waiting {
				break waitForTableLock
			}
		case <-lockDeadline.C:
			t.Fatal("key adoption did not wait on the legacy encrypted writer")
		}
	}

	if err := writer.Commit(t.Context()); err != nil {
		t.Fatalf("commit legacy encrypted write: %v", err)
	}
	select {
	case err := <-finished:
		if err == nil || !strings.Contains(err.Error(), "does not match existing data") {
			t.Fatalf("key adoption after legacy write: err = %v, want existing-data mismatch", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("key adoption remained blocked after the legacy writer committed")
	}

	var sentinelCount int
	if err := store.pool.QueryRow(t.Context(), `SELECT COUNT(*) FROM encryption_key_metadata`).Scan(&sentinelCount); err != nil {
		t.Fatalf("count encryption key sentinels: %v", err)
	}
	if sentinelCount != 0 {
		t.Fatalf("sentinel count = %d, want no sentinel for a rejected key", sentinelCount)
	}
}

func TestConcurrentFirstStartWithSameEncryptionKeySucceeds(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	if err := store.migrate(t.Context()); err != nil {
		t.Fatalf("migrate isolated store: %v", err)
	}
	key := bytes.Repeat([]byte{5}, 32)
	stores := []*Store{
		{pool: store.pool, secretKey: key},
		{pool: store.pool, secretKey: key},
	}
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	for _, candidate := range stores {
		go func() {
			<-start
			errs <- candidate.verifyEncryptionKey(t.Context())
		}()
	}
	close(start)
	for range stores {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent first start with the same key: %v", err)
		}
	}
}

func TestConcurrentFirstStartWithDifferentEncryptionKeysRejectsOne(t *testing.T) {
	store := newMigrationIntegrationStore(t)
	if err := store.migrate(t.Context()); err != nil {
		t.Fatalf("migrate isolated store: %v", err)
	}
	stores := []*Store{
		{pool: store.pool, secretKey: bytes.Repeat([]byte{6}, 32)},
		{pool: store.pool, secretKey: bytes.Repeat([]byte{7}, 32)},
	}
	start := make(chan struct{})
	errs := make(chan error, len(stores))
	for _, candidate := range stores {
		go func() {
			<-start
			errs <- candidate.verifyEncryptionKey(t.Context())
		}()
	}
	close(start)

	succeeded := 0
	rejected := 0
	for range stores {
		err := <-errs
		switch {
		case err == nil:
			succeeded++
		case strings.Contains(err.Error(), "does not match this database"):
			rejected++
		default:
			t.Fatalf("concurrent first start returned unexpected error: %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("concurrent first starts: succeeded = %d, rejected = %d; want one of each", succeeded, rejected)
	}
}
