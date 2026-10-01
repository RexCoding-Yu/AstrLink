package controlapi

import (
	"context"
	"net/http"
)

type localSocketAuthKey struct{}

// ContextWithLocalSocketAuth marks a request as arriving on the same-uid
// local control socket. The socket file mode and peer-credential check
// authenticate it as an observer: agent tools on the socket may read, but the
// operator role still requires the per-start bearer token.
func ContextWithLocalSocketAuth(ctx context.Context) context.Context {
	return context.WithValue(ctx, localSocketAuthKey{}, true)
}

// LocalSocketAuthenticated reports whether the request was accepted on the
// local control socket. Such a request holds RoleObserver.
func LocalSocketAuthenticated(request *http.Request) bool {
	if request == nil {
		return false
	}
	value, _ := request.Context().Value(localSocketAuthKey{}).(bool)
	return value
}

// LocalSocketHandler wraps a control handler so every request is treated as
// locally socket-authenticated, which grants RoleObserver.
func LocalSocketHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		next.ServeHTTP(writer, request.WithContext(ContextWithLocalSocketAuth(request.Context())))
	})
}
