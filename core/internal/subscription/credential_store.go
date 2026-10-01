package subscription

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
)

// StorageAccountCredentialStore keeps each account's OAuth tokens in the
// database, sealed under the local key, at local://subscription/<id> (plan
// §5.9). It is available whenever the store is open: the local key was
// resolved before the store opened.
//
// Accounts whose tokens could not leave the OS keystore yet keep reading and
// deleting them there until a later start moves them; see
// MigrateKeyringCredentials.
type StorageAccountCredentialStore struct {
	store secretstore.SecretStore

	mu      sync.Mutex
	legacy  accountauth.AccountCredentialStore
	pending map[contract.SubscriptionAccountID]struct{}
}

func NewStorageAccountCredentialStore(store secretstore.SecretStore) *StorageAccountCredentialStore {
	return &StorageAccountCredentialStore{store: store, pending: make(map[contract.SubscriptionAccountID]struct{})}
}

func subscriptionCredentialRef(id contract.SubscriptionAccountID) (secretstore.Ref, error) {
	if err := id.Validate(); err != nil {
		return "", err
	}
	return secretstore.Ref(accountauth.CredentialRefFor(id)), nil
}

func (credentials *StorageAccountCredentialStore) Available(context.Context) error {
	if credentials == nil || credentials.store == nil {
		return accountauth.ErrCredentialStoreUnavailable
	}
	return nil
}

func (credentials *StorageAccountCredentialStore) Get(ctx context.Context, id contract.SubscriptionAccountID) (accountauth.AccountTokens, error) {
	ref, err := subscriptionCredentialRef(id)
	if err != nil {
		return accountauth.AccountTokens{}, err
	}
	raw, err := credentials.store.Get(ctx, ref)
	if err == nil {
		defer clear(raw)
		return accountauth.UnmarshalAccountTokens(raw)
	}
	if !errors.Is(err, secretstore.ErrNotFound) {
		return accountauth.AccountTokens{}, accountCredentialError(err)
	}
	if legacy := credentials.legacyFor(id); legacy != nil {
		return legacy.Get(ctx, id)
	}
	return accountauth.AccountTokens{}, accountauth.ErrCredentialNotFound
}

func (credentials *StorageAccountCredentialStore) Put(ctx context.Context, id contract.SubscriptionAccountID, tokens accountauth.AccountTokens) error {
	ref, err := subscriptionCredentialRef(id)
	if err != nil {
		return err
	}
	raw, err := tokens.MarshalSecret()
	if err != nil {
		return err
	}
	defer clear(raw)
	if err := credentials.store.Put(ctx, ref, raw); err != nil {
		return accountCredentialError(err)
	}
	// The database copy is now the newest; the keystore copy is only stale.
	if legacy := credentials.legacyFor(id); legacy != nil && legacy.Delete(ctx, id) == nil {
		credentials.forgetLegacy(id)
	}
	return nil
}

func (credentials *StorageAccountCredentialStore) Delete(ctx context.Context, id contract.SubscriptionAccountID) error {
	ref, err := subscriptionCredentialRef(id)
	if err != nil {
		return err
	}
	if err := credentials.store.Delete(ctx, ref); err != nil && !errors.Is(err, secretstore.ErrNotFound) {
		return accountCredentialError(err)
	}
	if legacy := credentials.legacyFor(id); legacy != nil {
		if err := legacy.Delete(ctx, id); err != nil {
			return err
		}
		credentials.forgetLegacy(id)
	}
	return nil
}

func (credentials *StorageAccountCredentialStore) legacyFor(id contract.SubscriptionAccountID) accountauth.AccountCredentialStore {
	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	if _, ok := credentials.pending[id]; !ok {
		return nil
	}
	return credentials.legacy
}

func (credentials *StorageAccountCredentialStore) forgetLegacy(id contract.SubscriptionAccountID) {
	credentials.mu.Lock()
	defer credentials.mu.Unlock()
	delete(credentials.pending, id)
}

func accountCredentialError(err error) error {
	if errors.Is(err, secretstore.ErrUnavailable) {
		return fmt.Errorf("%w: %v", accountauth.ErrCredentialStoreUnavailable, err)
	}
	return err
}

// MigrateKeyringCredentials moves the OAuth tokens of every account that
// still references the OS keystore into the database. Per account it copies
// the tokens, deletes the keystore entry only once the copy is stored, and
// then points credential_ref at local://subscription/<id>. A step that fails
// logs and is retried at the next start; until then the account keeps
// reading and deleting its tokens in the keystore. It returns how many
// accounts moved. Run it before the subscription manager serves requests.
func (credentials *StorageAccountCredentialStore) MigrateKeyringCredentials(
	ctx context.Context,
	accounts AccountStore,
	keyring accountauth.AccountCredentialStore,
	logf func(string, ...any),
) int {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	listed, err := accounts.ListAccounts(ctx)
	if err != nil {
		logf("astrlink subscription: list accounts for credential migration: %v", err)
		return 0
	}
	moved := 0
	for _, account := range listed {
		if !strings.HasPrefix(account.CredentialRef, "keyring://") {
			continue
		}
		if err := credentials.migrateAccount(ctx, accounts, keyring, account); err != nil {
			credentials.mu.Lock()
			credentials.legacy = keyring
			credentials.pending[account.ID] = struct{}{}
			credentials.mu.Unlock()
			logf("astrlink subscription: account %s keeps its OS keystore credential until the next start: %v", account.ID, err)
			continue
		}
		moved++
	}
	return moved
}

func (credentials *StorageAccountCredentialStore) migrateAccount(
	ctx context.Context,
	accounts AccountStore,
	keyring accountauth.AccountCredentialStore,
	account contract.SubscriptionAccount,
) error {
	ref, err := subscriptionCredentialRef(account.ID)
	if err != nil {
		return err
	}
	// A database copy already exists when an earlier start moved the tokens
	// but stopped before rewriting the reference, or a refresh wrote them
	// back since. It is never older than the keystore copy.
	existing, err := credentials.store.Get(ctx, ref)
	clear(existing)
	switch {
	case errors.Is(err, secretstore.ErrNotFound):
		tokens, err := keyring.Get(ctx, account.ID)
		if errors.Is(err, accountauth.ErrCredentialNotFound) {
			// Nothing left to move; the account signs in again either way.
			break
		}
		if err != nil {
			return fmt.Errorf("read OS keystore: %w", err)
		}
		if err := credentials.Put(ctx, account.ID, tokens); err != nil {
			return fmt.Errorf("store credential: %w", err)
		}
	case err != nil:
		return fmt.Errorf("read stored credential: %w", err)
	}
	// The reference changes last, so a keystore entry that failed to go is
	// retried at the next start. Reads already prefer the database copy.
	if err := keyring.Delete(ctx, account.ID); err != nil {
		return fmt.Errorf("delete OS keystore entry: %w", err)
	}
	account.CredentialRef = string(ref)
	if err := accounts.PutAccount(ctx, account); err != nil {
		return fmt.Errorf("update credential_ref: %w", err)
	}
	return nil
}
