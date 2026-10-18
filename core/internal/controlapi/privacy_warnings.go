package controlapi

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

// PrivacyWarningsPath serves the requests the warn action let through, so the
// desktop can notify about them. Core never pushes; the desktop polls it with
// the last sequence it read.
const PrivacyWarningsPath = "/control/v1/privacy/warnings"

// PrivacyWarningFeed is satisfied by *privacy.WarningFeed.
type PrivacyWarningFeed interface {
	Since(uint64) privacy.WarningPage
}

func (handler *Handler) getPrivacyWarnings(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
		return
	}
	var since uint64
	if raw := request.URL.Query().Get("since"); raw != "" {
		parsed, err := strconv.ParseUint(raw, 10, 64)
		if err != nil {
			writeError(writer, http.StatusBadRequest, "invalid_query", "since must be a non-negative integer")
			return
		}
		since = parsed
	}
	writeJSON(writer, http.StatusOK, handler.privacyWarnings.Since(since))
}
