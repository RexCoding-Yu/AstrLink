package controlapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthenticatedAcceptsLocalSocketWithoutBearer(t *testing.T) {
	handler := &Handler{controlToken: []byte("control-token-16b")}
	protected := handler.authenticated(func(writer http.ResponseWriter, request *http.Request) {
		if role := requestRole(request); role != RoleObserver {
			t.Fatalf("role = %s, want observer", role)
		}
		writer.WriteHeader(http.StatusNoContent)
	}, RoleObserver)

	request := httptest.NewRequest(http.MethodGet, "/control/v1/requests", nil)
	request = request.WithContext(ContextWithLocalSocketAuth(request.Context()))
	recorder := httptest.NewRecorder()
	protected(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
}

func TestAuthenticatedKeepsOperatorRoutesFromLocalSocket(t *testing.T) {
	handler := &Handler{controlToken: []byte("control-token-16b")}
	for _, test := range []struct {
		method  string
		minRole Role
	}{
		{http.MethodGet, RoleOperator},
		{http.MethodPost, RoleObserver},
		{http.MethodPatch, RoleObserver},
		{http.MethodDelete, RoleObserver},
	} {
		protected := handler.authenticated(func(http.ResponseWriter, *http.Request) {
			t.Fatalf("%s handler must not run for a socket caller", test.method)
		}, test.minRole)
		request := httptest.NewRequest(test.method, "/control/v1/requests", nil)
		request = request.WithContext(ContextWithLocalSocketAuth(request.Context()))
		recorder := httptest.NewRecorder()
		protected(recorder, request)
		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403", test.method, recorder.Code)
		}
	}
}

func TestAuthenticatedRejectsHTTPWithoutBearer(t *testing.T) {
	handler := &Handler{controlToken: []byte("control-token-16b")}
	protected := handler.authenticated(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler must not run")
	}, RoleObserver)

	request := httptest.NewRequest(http.MethodGet, "/control/v1/requests", nil)
	recorder := httptest.NewRecorder()
	protected(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
}

func TestAuthenticatedAcceptsMatchingBearer(t *testing.T) {
	token := "control-token-16b"
	handler := &Handler{controlToken: []byte(token)}
	protected := handler.authenticated(func(writer http.ResponseWriter, request *http.Request) {
		if role := requestRole(request); role != RoleOperator {
			t.Fatalf("role = %s, want operator", role)
		}
		writer.WriteHeader(http.StatusNoContent)
	}, RoleOperator)

	request := httptest.NewRequest(http.MethodPost, "/control/v1/requests", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	protected(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
}
