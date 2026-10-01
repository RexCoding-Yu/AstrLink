package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/secretstore"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

func applyProxyCredentialMutation(service contract.Service, mutation storage.CredentialMutation) (contract.Service, error) {
	if service.Proxy == nil || service.Proxy.Mode != "custom" {
		if mutation.Proxy != nil {
			return service, fmt.Errorf("proxy authentication requires custom mode")
		}
		return service, nil
	}
	proxy := *service.Proxy
	service.Proxy = &proxy
	if mutation.ProxyPresent {
		proxy.CredentialRef = ""
		if mutation.Proxy != nil {
			if err := mutation.Proxy.Validate(); err != nil {
				return service, err
			}
			proxy.CredentialRef = "local://service-proxy/" + string(service.ID)
		}
	}
	return service, nil
}

func (store *Store) putProxyCredentialTx(ctx context.Context, tx *sql.Tx, service contract.Service, mutation storage.CredentialMutation) error {
	if service.Proxy == nil || service.Proxy.Mode != "custom" || (mutation.ProxyPresent && mutation.Proxy == nil) {
		_, err := tx.ExecContext(ctx, `DELETE FROM service_proxy_credentials WHERE service_id = ?`, service.ID)
		return err
	}
	if !mutation.ProxyPresent {
		return nil
	}
	value, err := json.Marshal(mutation.Proxy)
	if err != nil {
		return err
	}
	defer clear(value)
	sealed, err := store.keys.sealColumn(serviceProxyCredentialsTable, string(service.ID), value)
	if err != nil {
		return fmt.Errorf("seal proxy credential: %w", err)
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO service_proxy_credentials (service_id, credential_value, sealed) VALUES (?, ?, 1) ON CONFLICT(service_id) DO UPDATE SET credential_value=excluded.credential_value, sealed=1`, service.ID, sealed)
	return err
}

func (store *Store) getProxyCredential(ctx context.Context, ref secretstore.Ref) ([]byte, error) {
	id := contract.ServiceID(strings.TrimPrefix(string(ref), "local://service-proxy/"))
	if err := id.Validate(); err != nil {
		return nil, secretstore.ErrNotFound
	}
	var value []byte
	var sealed bool
	if err := store.db.QueryRowContext(ctx, `SELECT credential_value, sealed FROM service_proxy_credentials WHERE service_id = ?`, id).Scan(&value, &sealed); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, secretstore.ErrNotFound
		}
		return nil, secretstore.ErrUnavailable
	}
	return store.openSecret(serviceProxyCredentialsTable, string(id), value, sealed)
}
