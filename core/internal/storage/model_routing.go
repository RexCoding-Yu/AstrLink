package storage

import (
	"context"
	"github.com/QuantumNous/astrlink/core/contract"
)

type ModelRoutingBindingStore interface {
	GetModelRoutingBinding(context.Context, string, contract.SessionID, contract.ProtocolID, string) (contract.ModelRoutingBinding, error)
	UpsertModelRoutingBinding(context.Context, contract.ModelRoutingBinding) error
	DeleteModelRoutingBinding(context.Context, string, contract.SessionID, contract.ProtocolID, string) error
}

// ModelRoutingWriteCoordinator is the atomic settings write seam. A store that
// implements it validates the referenced service/model closure in the same
// write transaction as the settings document.
type ModelRoutingWriteCoordinator interface {
	UpdateRoutingSettingsAtomic(context.Context, contract.RoutingSettings) error
}
