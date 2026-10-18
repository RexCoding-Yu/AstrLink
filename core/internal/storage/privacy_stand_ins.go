package storage

import (
	"context"

	"github.com/QuantumNous/astrlink/core/contract"
)

// PrivacyStandIn is one IPv4 stand-in a conversation issued. Owner is the
// keyed fingerprint of the value it stands for, never the value.
type PrivacyStandIn struct {
	StandIn string
	Owner   string
}

// PrivacyStandInStore keeps each conversation's IPv4 stand-ins across
// restarts. They go once none of the conversation's request records are left.
type PrivacyStandInStore interface {
	// PrivacyStandIns lists the conversation's stand-ins, oldest first.
	PrivacyStandIns(context.Context, contract.SessionID) ([]PrivacyStandIn, error)
	// RememberPrivacyStandIns adds stand-ins the conversation issued. One
	// already recorded keeps its owner.
	RememberPrivacyStandIns(context.Context, contract.SessionID, []PrivacyStandIn) error
}
