package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// LocalDataStore reports saved secrets and bodies the local key no longer
// opens (plan §5.7).
type LocalDataStore interface {
	LocalDataStatus(context.Context) (contract.LocalDataStatus, error)
}
