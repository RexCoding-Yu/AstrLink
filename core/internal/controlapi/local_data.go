package controlapi

import "net/http"

// LocalDataPath reports saved data this device cannot decrypt, such as
// credentials sealed under a local key that is gone (plan §5.7). It carries
// counts only, never names or values.
const LocalDataPath = "/control/v1/local-data"

func (handler *Handler) registerLocalDataRoutes() {
	handler.mux.HandleFunc(LocalDataPath, handler.authenticated(handler.getOnly(handler.getLocalData), RoleObserver))
}

func (handler *Handler) getLocalData(writer http.ResponseWriter, request *http.Request) {
	if request.URL.RawQuery != "" {
		writeError(writer, http.StatusBadRequest, "invalid_query", "local data status does not accept query parameters")
		return
	}
	status, err := handler.localData.LocalDataStatus(request.Context())
	if err != nil {
		handler.writeStoreError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, status)
}
