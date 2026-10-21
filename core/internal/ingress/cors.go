package ingress

import (
	"net/http"
	"strings"
)

// Preflights carry no API token and perform no inference. Actual requests still
// go through the normal token gate; cookies never authenticate the API.
func (handler *Handler) servePreflight(writer http.ResponseWriter, request *http.Request) {
	if !handler.hostAllowed(request.Host) {
		writeInferenceError(writer, http.StatusMisdirectedRequest, "host_forbidden", "request Host does not match the inference listener", false, nil)
		return
	}
	route, _, known := matchProtocolRoute(request.URL.Path)
	method := route.method
	if request.URL.Path == ReachabilityPath {
		method, known = http.MethodGet, true
	}
	if !known {
		writeInferenceError(writer, http.StatusNotFound, "not_found", "inference endpoint not found", false, nil)
		return
	}
	if requested := request.Header.Get("Access-Control-Request-Method"); requested != "" && requested != method {
		writer.Header().Set("Allow", method)
		writeInferenceError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "method is not allowed for this inference endpoint", false, nil)
		return
	}
	writer.Header().Set("Access-Control-Allow-Methods", method)
	writer.Header().Set("Access-Control-Allow-Headers", strings.Join(request.Header.Values("Access-Control-Request-Headers"), ", "))
	writer.Header().Add("Vary", "Access-Control-Request-Method, Access-Control-Request-Headers, Access-Control-Request-Private-Network")
	if request.Header.Get("Access-Control-Request-Private-Network") == "true" {
		writer.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	writer.Header().Set("Access-Control-Max-Age", "600")
	writer.WriteHeader(http.StatusNoContent)
}
