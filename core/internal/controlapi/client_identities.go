package controlapi

import (
	"net/http"

	"github.com/QuantumNous/astrlink/core/contract"
)

// ClientIdentitiesPath reports the Codex and Claude Code versions learned
// from official client requests next to the built-in ones.
const ClientIdentitiesPath = "/control/v1/client-identities"

// ClientIdentityReporter is satisfied by *accountauth.IdentityRegistry.
type ClientIdentityReporter interface {
	ClientIdentities() contract.ClientIdentities
}

func (handler *Handler) registerClientIdentityRoutes() {
	handler.mux.HandleFunc(ClientIdentitiesPath, handler.authenticated(handler.getOnly(handler.getClientIdentities), RoleObserver))
}

func (handler *Handler) getClientIdentities(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "client identities do not accept query parameters")
		return
	}
	writeJSON(writer, http.StatusOK, handler.clientIdentities.ClientIdentities())
}
