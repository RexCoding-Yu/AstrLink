package subscription_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

// fakeSecretStore stands in for the database; err fails every call.
type fakeSecretStore struct {
	mu     sync.Mutex
	values map[secretstore.Ref][]byte
	err    error
}

func newFakeSecretStore() *fakeSecretStore {
	return &fakeSecretStore{values: map[secretstore.Ref][]byte{}}
}

func (store *fakeSecretStore) Get(_ context.Context, ref secretstore.Ref) ([]byte, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return nil, store.err
	}
	value, ok := store.values[ref]
	if !ok {
		return nil, secretstore.ErrNotFound
	}
	return append([]byte(nil), value...), nil
}

func (store *fakeSecretStore) Put(_ context.Context, ref secretstore.Ref, value []byte) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return store.err
	}
	store.values[ref] = append([]byte(nil), value...)
	return nil
}

func (store *fakeSecretStore) Delete(_ context.Context, ref secretstore.Ref) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.err != nil {
		return store.err
	}
	if _, ok := store.values[ref]; !ok {
		return secretstore.ErrNotFound
	}
	delete(store.values, ref)
	return nil
}

// fakeKeyring stands in for the OS keystore. It never touches a real one.
type fakeKeyring struct {
	*accountauth.MemoryCredentialStore
	getErr, deleteErr error
	gets, deletes     int
}

func newFakeKeyring() *fakeKeyring {
	return &fakeKeyring{MemoryCredentialStore: accountauth.NewMemoryCredentialStore()}
}

func (keyring *fakeKeyring) Get(ctx context.Context, id contract.SubscriptionAccountID) (accountauth.AccountTokens, error) {
	keyring.gets++
	if keyring.getErr != nil {
		return accountauth.AccountTokens{}, keyring.getErr
	}
	return keyring.MemoryCredentialStore.Get(ctx, id)
}

func (keyring *fakeKeyring) Delete(ctx context.Context, id contract.SubscriptionAccountID) error {
	keyring.deletes++
	if keyring.deleteErr != nil {
		return keyring.deleteErr
	}
	return keyring.MemoryCredentialStore.Delete(ctx, id)
}

type failingAccountStore struct {
	subscription.AccountStore
	putErr error
}

func (accounts *failingAccountStore) PutAccount(ctx context.Context, account contract.SubscriptionAccount) error {
	if accounts.putErr != nil {
		return accounts.putErr
	}
	return accounts.AccountStore.PutAccount(ctx, account)
}

type migrationFixture struct {
	accounts *failingAccountStore
	keyring  *fakeKeyring
	secrets  *fakeSecretStore
	store    *subscription.StorageAccountCredentialStore
	logs     []string
	tokens   accountauth.AccountTokens
}

const migratingAccount = contract.ServiceID("service_codex_keyring")

func legacyRef(id contract.ServiceID) string { return "keyring://astrlink/subscription/" + string(id) }

// newMigrationFixture stores one connected account whose tokens are still in
// the keystore, as the release before this one left it.
func newMigrationFixture(t *testing.T) *migrationFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	fixture := &migrationFixture{
		accounts: &failingAccountStore{AccountStore: subscription.NewMemoryAccountStore()},
		keyring:  newFakeKeyring(),
		secrets:  newFakeSecretStore(),
		tokens: accountauth.AccountTokens{
			AccessToken: "keyring-access-token", RefreshToken: "keyring-refresh-token",
			AccountID: "acct_keyring_123456", ExpiresAt: now.Add(time.Hour),
		},
	}
	fixture.store = subscription.NewStorageAccountCredentialStore(fixture.secrets)
	account := connectedAccount(migratingAccount, now, now.Add(time.Hour), "acct_keyring_123456")
	account.CredentialRef = legacyRef(migratingAccount)
	if err := fixture.accounts.PutAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if err := fixture.keyring.Put(ctx, migratingAccount, fixture.tokens); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *migrationFixture) migrate() int {
	return fixture.store.MigrateKeyringCredentials(context.Background(), fixture.accounts, fixture.keyring,
		func(format string, args ...any) { fixture.logs = append(fixture.logs, fmt.Sprintf(format, args...)) })
}

func (fixture *migrationFixture) ref(t *testing.T) string {
	t.Helper()
	account, err := fixture.accounts.GetAccount(context.Background(), migratingAccount)
	if err != nil {
		t.Fatal(err)
	}
	return account.CredentialRef
}

func (fixture *migrationFixture) inKeyring(t *testing.T) bool {
	t.Helper()
	_, err := fixture.keyring.MemoryCredentialStore.Get(context.Background(), migratingAccount)
	if err != nil && !errors.Is(err, accountauth.ErrCredentialNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

func (fixture *migrationFixture) stored(t *testing.T) (accountauth.AccountTokens, bool) {
	t.Helper()
	raw, err := fixture.secrets.Get(context.Background(), secretstore.Ref(accountauth.CredentialRefFor(migratingAccount)))
	if errors.Is(err, secretstore.ErrNotFound) {
		return accountauth.AccountTokens{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := accountauth.UnmarshalAccountTokens(raw)
	if err != nil {
		t.Fatal(err)
	}
	return tokens, true
}

func TestMigrateKeyringCredentialsMovesTokensOutOfTheKeystoreOnce(t *testing.T) {
	ctx := context.Background()
	fixture := newMigrationFixture(t)
	now := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	// An account already on the database and one never connected are left
	// alone.
	local := connectedAccount("service_codex_local", now, now.Add(time.Hour), "acct_local_123456")
	if err := fixture.accounts.PutAccount(ctx, local); err != nil {
		t.Fatal(err)
	}
	if err := fixture.accounts.PutAccount(ctx, disconnectedAccount("service_codex_idle", now)); err != nil {
		t.Fatal(err)
	}

	if moved := fixture.migrate(); moved != 1 || len(fixture.logs) != 0 {
		t.Fatalf("moved = %d, logs = %q", moved, fixture.logs)
	}
	if stored, ok := fixture.stored(t); !ok || stored != fixture.tokens {
		t.Fatal("the keystore tokens were not stored in the database")
	}
	if fixture.inKeyring(t) {
		t.Fatal("the keystore entry survived the move")
	}
	if got := fixture.ref(t); got != "local://subscription/"+string(migratingAccount) {
		t.Fatalf("credential_ref = %q", got)
	}
	if got, err := fixture.store.Get(ctx, migratingAccount); err != nil || got != fixture.tokens {
		t.Fatalf("Get after the move = %v", err)
	}
	if fixture.keyring.gets != 1 || fixture.keyring.deletes != 1 {
		t.Fatalf("keystore calls = %d gets, %d deletes; want 1 each", fixture.keyring.gets, fixture.keyring.deletes)
	}

	// The next start finds nothing to move and never asks the keystore.
	if moved := fixture.migrate(); moved != 0 || len(fixture.logs) != 0 {
		t.Fatalf("second run moved = %d, logs = %q", moved, fixture.logs)
	}
	if fixture.keyring.gets != 1 || fixture.keyring.deletes != 1 {
		t.Fatalf("second run called the keystore: %d gets, %d deletes", fixture.keyring.gets, fixture.keyring.deletes)
	}
	if err := fixture.store.Delete(ctx, migratingAccount); err != nil || fixture.keyring.deletes != 1 {
		t.Fatalf("Delete of a moved account = %v, keystore deletes = %d", err, fixture.keyring.deletes)
	}
}

func TestMigrateKeyringCredentialsRewritesTheRefWhenTheKeystoreIsEmpty(t *testing.T) {
	fixture := newMigrationFixture(t)
	if err := fixture.keyring.MemoryCredentialStore.Delete(context.Background(), migratingAccount); err != nil {
		t.Fatal(err)
	}
	if moved := fixture.migrate(); moved != 1 || len(fixture.logs) != 0 {
		t.Fatalf("moved = %d, logs = %q", moved, fixture.logs)
	}
	if _, ok := fixture.stored(t); ok {
		t.Fatal("a credential appeared from an empty keystore")
	}
	if got := fixture.ref(t); got != accountauth.CredentialRefFor(migratingAccount) {
		t.Fatalf("credential_ref = %q", got)
	}
	if _, err := fixture.store.Get(context.Background(), migratingAccount); !errors.Is(err, accountauth.ErrCredentialNotFound) {
		t.Fatalf("Get = %v, want ErrCredentialNotFound", err)
	}
}

func TestMigrateKeyringCredentialsKeepsTheKeystoreCopyWhenItCannotRead(t *testing.T) {
	ctx := context.Background()
	fixture := newMigrationFixture(t)
	fixture.keyring.getErr = accountauth.ErrCredentialStoreUnavailable
	if moved := fixture.migrate(); moved != 0 || len(fixture.logs) != 1 ||
		!strings.Contains(fixture.logs[0], string(migratingAccount)) ||
		!strings.Contains(fixture.logs[0], "read OS keystore") {
		t.Fatalf("moved = %d, logs = %q", moved, fixture.logs)
	}
	if strings.Contains(fixture.logs[0], fixture.tokens.RefreshToken) || strings.Contains(fixture.logs[0], fixture.tokens.AccessToken) {
		t.Fatal("the migration log carries a token")
	}
	if got := fixture.ref(t); got != legacyRef(migratingAccount) {
		t.Fatalf("credential_ref = %q after a failed move", got)
	}
	if !fixture.inKeyring(t) {
		t.Fatal("a failed move deleted the keystore entry")
	}

	// Until the next start the account keeps reading its keystore copy.
	fixture.keyring.getErr = nil
	if got, err := fixture.store.Get(ctx, migratingAccount); err != nil || got != fixture.tokens {
		t.Fatalf("Get of a pending account = %v", err)
	}
	// A refresh writes the database copy and drops the stale keystore one.
	rotated := fixture.tokens
	rotated.RefreshToken = "rotated-refresh-token"
	if err := fixture.store.Put(ctx, migratingAccount, rotated); err != nil {
		t.Fatal(err)
	}
	if stored, ok := fixture.stored(t); !ok || stored != rotated || fixture.inKeyring(t) {
		t.Fatal("Put did not replace the keystore copy with a database copy")
	}
	// The next start finds the database copy and only rewrites the ref.
	fixture.logs = nil
	if moved := fixture.migrate(); moved != 1 || len(fixture.logs) != 0 {
		t.Fatalf("retry moved = %d, logs = %q", moved, fixture.logs)
	}
	if stored, _ := fixture.stored(t); stored != rotated || fixture.ref(t) != accountauth.CredentialRefFor(migratingAccount) {
		t.Fatal("the retry did not finish the move")
	}
}

func TestMigrateKeyringCredentialsRetriesAKeystoreEntryThatWouldNotDelete(t *testing.T) {
	ctx := context.Background()
	fixture := newMigrationFixture(t)
	fixture.keyring.deleteErr = accountauth.ErrCredentialStoreUnavailable
	if moved := fixture.migrate(); moved != 0 || len(fixture.logs) != 1 || !strings.Contains(fixture.logs[0], "delete OS keystore entry") {
		t.Fatalf("moved = %d, logs = %q", moved, fixture.logs)
	}
	if stored, ok := fixture.stored(t); !ok || stored != fixture.tokens {
		t.Fatal("the copy was not kept in the database")
	}
	if got := fixture.ref(t); got != legacyRef(migratingAccount) {
		t.Fatalf("credential_ref = %q while the keystore entry remains", got)
	}
	// Sign-out must not report success while the keystore still holds tokens.
	if err := fixture.store.Delete(ctx, migratingAccount); !errors.Is(err, accountauth.ErrCredentialStoreUnavailable) {
		t.Fatalf("Delete with a stuck keystore entry = %v", err)
	}

	// A newer database copy written meanwhile survives the retry.
	rotated := fixture.tokens
	rotated.AccessToken = "rotated-access-token"
	if err := fixture.store.Put(ctx, migratingAccount, rotated); err != nil {
		t.Fatal(err)
	}
	fixture.keyring.deleteErr = nil
	fixture.logs = nil
	if moved := fixture.migrate(); moved != 1 || len(fixture.logs) != 0 {
		t.Fatalf("retry moved = %d, logs = %q", moved, fixture.logs)
	}
	if stored, _ := fixture.stored(t); stored != rotated {
		t.Fatal("the retry overwrote the newer database copy")
	}
	if fixture.inKeyring(t) || fixture.ref(t) != accountauth.CredentialRefFor(migratingAccount) {
		t.Fatal("the retry did not finish the move")
	}
}

func TestMigrateKeyringCredentialsRetriesARefThatWouldNotSave(t *testing.T) {
	ctx := context.Background()
	fixture := newMigrationFixture(t)
	fixture.accounts.putErr = errors.New("database is locked")
	if moved := fixture.migrate(); moved != 0 || len(fixture.logs) != 1 || !strings.Contains(fixture.logs[0], "update credential_ref") {
		t.Fatalf("moved = %d, logs = %q", moved, fixture.logs)
	}
	if got, err := fixture.store.Get(ctx, migratingAccount); err != nil || got != fixture.tokens {
		t.Fatalf("Get with the database copy but the old ref = %v", err)
	}
	fixture.accounts.putErr = nil
	fixture.logs = nil
	if moved := fixture.migrate(); moved != 1 || len(fixture.logs) != 0 {
		t.Fatalf("retry moved = %d, logs = %q", moved, fixture.logs)
	}
	if fixture.ref(t) != accountauth.CredentialRefFor(migratingAccount) {
		t.Fatal("the retry did not rewrite the ref")
	}
}

func TestStorageAccountCredentialStoreReportsAnUnreadableDatabase(t *testing.T) {
	ctx := context.Background()
	fixture := newMigrationFixture(t)
	fixture.secrets.err = fmt.Errorf("%w: subscription_credentials does not decrypt on this device", secretstore.ErrUnavailable)
	if moved := fixture.migrate(); moved != 0 || len(fixture.logs) != 1 || !strings.Contains(fixture.logs[0], "read stored credential") {
		t.Fatalf("moved = %d, logs = %q", moved, fixture.logs)
	}
	if !fixture.inKeyring(t) || fixture.ref(t) != legacyRef(migratingAccount) {
		t.Fatal("a failed move changed the keystore entry or the ref")
	}
	if _, err := fixture.store.Get(ctx, migratingAccount); !errors.Is(err, accountauth.ErrCredentialStoreUnavailable) {
		t.Fatalf("Get = %v, want ErrCredentialStoreUnavailable", err)
	}
	if err := fixture.store.Put(ctx, migratingAccount, fixture.tokens); !errors.Is(err, accountauth.ErrCredentialStoreUnavailable) {
		t.Fatalf("Put = %v, want ErrCredentialStoreUnavailable", err)
	}
	if _, err := fixture.store.Get(ctx, "BAD ID"); err == nil {
		t.Fatal("Get accepted an invalid account ID")
	}
	if err := fixture.store.Available(ctx); err != nil {
		t.Fatalf("Available = %v", err)
	}
	if err := subscription.NewStorageAccountCredentialStore(nil).Available(ctx); !errors.Is(err, accountauth.ErrCredentialStoreUnavailable) {
		t.Fatalf("Available without a store = %v", err)
	}
}

// A refresh after the move writes the rotated tokens to the database, sealed:
// neither token appears in the database files.
func TestRefreshWritesRotatedTokensBackSealed(t *testing.T) {
	var refreshRequest http.Header
	issuer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		refreshRequest = request.Header.Clone()
		if err := request.ParseForm(); err != nil || request.Form.Get("refresh_token") != "PLAINTEXT-refresh-before" {
			http.Error(writer, "bad refresh request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "PLAINTEXT-access-after", "refresh_token": "PLAINTEXT-refresh-after", "expires_in": 3600,
		})
	}))
	t.Cleanup(issuer.Close)

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "astrlink.db")
	store, err := sqlite.Open(ctx, path, sqlite.WithLocalKey(bytes.Repeat([]byte{0x61}, 32)), sqlite.WithLogger(func(string, ...any) {}))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// The store stamps rows with the wall clock, so the refresh must not run
	// earlier than the service was created.
	now := time.Now().UTC()
	id := contract.ServiceID("service_codex_sealed")
	if _, err := store.CreateService(ctx, contract.Service{
		ID: id, Name: "Codex", Kind: contract.ServiceKindCodexSubscription,
		Enabled: true, Models: []string{"gpt-5"}, Capabilities: contract.DefaultOpenAICodexCapabilities(),
		Subscription: &contract.SubscriptionConnection{
			Provider: contract.SubscriptionProviderOpenAICodex, Status: contract.SubscriptionStatusConnected,
			ProviderAccountID: "acct_sealed_123456", CredentialRef: legacyRef(id), TokenExpiresAt: &now,
		},
	}, storage.CredentialMutation{}); err != nil {
		t.Fatal(err)
	}
	keyring := newFakeKeyring()
	if err := keyring.Put(ctx, id, accountauth.AccountTokens{
		AccessToken: "PLAINTEXT-access-before", RefreshToken: "PLAINTEXT-refresh-before",
		AccountID: "acct_sealed_123456", ExpiresAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	accounts := subscription.StorageAccountStore{Store: store}
	credentials := subscription.NewStorageAccountCredentialStore(store)
	if moved := credentials.MigrateKeyringCredentials(ctx, accounts, keyring, t.Logf); moved != 1 {
		t.Fatalf("moved = %d", moved)
	}
	manager, err := subscription.NewManager(accounts, credentials, accountauth.OAuthConfig{
		ClientID: "astrlink_test_client", Issuer: issuer.URL, HTTPClient: issuer.Client(),
		Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := manager.AccessToken(ctx, id)
	if err != nil {
		t.Fatalf("AccessToken = %v", err)
	}
	if rotated.RefreshToken != "PLAINTEXT-refresh-after" {
		t.Fatal("the refresh did not rotate the tokens")
	}
	if stored, err := credentials.Get(ctx, id); err != nil || stored.RefreshToken != "PLAINTEXT-refresh-after" {
		t.Fatalf("stored tokens after the refresh = %v", err)
	}
	for name, values := range refreshRequest {
		for _, value := range values {
			if strings.Contains(strings.ToLower(name+value), "astrlink") {
				t.Fatalf("the refresh request carries %s: %s", name, value)
			}
		}
	}
	for _, suffix := range []string{"", "-wal"} {
		contents, err := os.ReadFile(path + suffix)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			t.Fatal(err)
		}
		if bytes.Contains(contents, []byte("PLAINTEXT-")) {
			t.Fatalf("astrlink.db%s holds an OAuth token in plaintext", suffix)
		}
	}
}
