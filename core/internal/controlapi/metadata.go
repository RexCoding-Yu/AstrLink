package controlapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/storage"
)

const MetadataAttributesPath = "/control/v1/metadata/attributes"

// MetadataRegistry keeps the attributes requests resolve against;
// *metadata.Registry satisfies it.
type MetadataRegistry interface {
	Attributes() []contract.MetadataAttribute
	Refresh(context.Context) error
	RegisterPlugin(ctx context.Context, plugin string, attributes []contract.MetadataAttribute) error
	TokenDefaults(contract.AccessTokenID) map[string]string
	Valid(key, value string) bool
}

func (handler *Handler) metadataAvailable(writer http.ResponseWriter) bool {
	if handler.metadata == nil || handler.metadataStore == nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable", "metadata is unavailable")
		return false
	}
	return true
}

func (handler *Handler) metadataAttributesResource(writer http.ResponseWriter, request *http.Request) {
	if !handler.metadataAvailable(writer) {
		return
	}
	key := strings.Trim(strings.TrimPrefix(request.URL.Path, MetadataAttributesPath), "/")
	if key == "" {
		switch request.Method {
		case http.MethodGet:
			handler.writeMetadataAttributes(writer)
		case http.MethodPost:
			handler.createMetadataAttribute(writer, request)
		default:
			writer.Header().Set("Allow", "GET, POST")
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or POST")
		}
		return
	}
	if !contract.ValidMetadataKey(key) {
		writeError(writer, http.StatusNotFound, "not_found", "metadata attribute not found")
		return
	}
	current, found := handler.metadataAttribute(key)
	if !found {
		writeError(writer, http.StatusNotFound, "not_found", "metadata attribute not found")
		return
	}
	switch request.Method {
	case http.MethodPatch:
		handler.updateMetadataAttribute(writer, request, current)
	case http.MethodDelete:
		handler.deleteMetadataAttribute(writer, request, current)
	default:
		writer.Header().Set("Allow", "PATCH, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use PATCH or DELETE")
	}
}

func (handler *Handler) metadataAttribute(key string) (contract.MetadataAttribute, bool) {
	for _, attribute := range handler.metadata.Attributes() {
		if attribute.Key == key {
			return attribute, true
		}
	}
	return contract.MetadataAttribute{}, false
}

func (handler *Handler) metadataView(attribute contract.MetadataAttribute) contract.MetadataAttributeView {
	view := contract.MetadataAttributeView{MetadataAttribute: attribute, Header: contract.MetadataHeader(attribute.Key)}
	if attribute.Owner != contract.MetadataOwnerUser && handler.plugins != nil {
		view.OwnerMissing = handler.plugins.Current().Plugin(attribute.Owner) == nil
	}
	return view
}

func (handler *Handler) writeMetadataAttributes(writer http.ResponseWriter) {
	attributes := handler.metadata.Attributes()
	views := make([]contract.MetadataAttributeView, 0, len(attributes))
	for _, attribute := range attributes {
		views = append(views, handler.metadataView(attribute))
	}
	writeJSON(writer, http.StatusOK, map[string]any{"attributes": views})
}

func (handler *Handler) createMetadataAttribute(writer http.ResponseWriter, request *http.Request) {
	var input contract.MetadataAttributeInput
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if len(handler.metadata.Attributes()) >= contract.MaxMetadataAttributes {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata_attribute", "there are at most 32 attributes")
		return
	}
	attribute := contract.MetadataAttribute{
		Key: input.Key, Label: input.Label, Description: input.Description, Pattern: input.Pattern, Marker: input.Marker,
		Owner: contract.MetadataOwnerUser,
	}
	if err := attribute.Validate(); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata_attribute", err.Error())
		return
	}
	if !handler.writeMetadataChange(writer, request.Context(), handler.metadataStore.CreateMetadataAttribute(request.Context(), attribute)) {
		return
	}
	created, _ := handler.metadataAttribute(attribute.Key)
	writeJSON(writer, http.StatusCreated, handler.metadataView(created))
}

func (handler *Handler) updateMetadataAttribute(writer http.ResponseWriter, request *http.Request, current contract.MetadataAttribute) {
	var input contract.MetadataAttributeInput
	if !decodeControlJSON(writer, request, &input) {
		return
	}
	if current.Owner != contract.MetadataOwnerUser {
		writeError(writer, http.StatusConflict, "metadata_attribute_owned", "the plugin "+current.Owner+" registered this attribute; only its values can be set")
		return
	}
	if input.Key != "" && input.Key != current.Key {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata_attribute", "the key of an attribute cannot change")
		return
	}
	attribute := current
	attribute.Label, attribute.Description, attribute.Pattern, attribute.Marker = input.Label, input.Description, input.Pattern, input.Marker
	if err := attribute.Validate(); err != nil {
		writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata_attribute", err.Error())
		return
	}
	if !handler.writeMetadataChange(writer, request.Context(), handler.metadataStore.UpdateMetadataAttribute(request.Context(), attribute)) {
		return
	}
	updated, _ := handler.metadataAttribute(attribute.Key)
	writeJSON(writer, http.StatusOK, handler.metadataView(updated))
}

func (handler *Handler) deleteMetadataAttribute(writer http.ResponseWriter, request *http.Request, current contract.MetadataAttribute) {
	if current.Owner != contract.MetadataOwnerUser && !handler.metadataView(current).OwnerMissing {
		writeError(writer, http.StatusConflict, "metadata_attribute_owned", "the plugin "+current.Owner+" registered this attribute; it can be deleted once the plugin is removed")
		return
	}
	if !handler.writeMetadataChange(writer, request.Context(), handler.metadataStore.DeleteMetadataAttribute(request.Context(), current.Key)) {
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

// writeMetadataChange answers a failed change, or refreshes the registry
// after a successful one; it reports whether the change went through.
func (handler *Handler) writeMetadataChange(writer http.ResponseWriter, ctx context.Context, err error) bool {
	switch {
	case errors.Is(err, storage.ErrConflict):
		writeError(writer, http.StatusConflict, "metadata_attribute_conflict", "another attribute has this key or marker")
		return false
	case errors.Is(err, storage.ErrInvalidArgument):
		writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata_attribute", strings.TrimPrefix(err.Error(), storage.ErrInvalidArgument.Error()+": "))
		return false
	case errors.Is(err, storage.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found", "metadata attribute not found")
		return false
	case err != nil:
		writeError(writer, http.StatusInternalServerError, "metadata_unavailable", "could not save the metadata")
		return false
	}
	if err := handler.metadata.Refresh(ctx); err != nil {
		writeError(writer, http.StatusInternalServerError, "metadata_unavailable", "saved, but the metadata could not be reloaded")
		return false
	}
	return true
}

// accessTokenDefaults serves the default metadata of an access token.
func (handler *Handler) accessTokenDefaults(writer http.ResponseWriter, request *http.Request, id contract.AccessTokenID) {
	if !handler.metadataAvailable(writer) {
		return
	}
	switch request.Method {
	case http.MethodGet:
		writeJSON(writer, http.StatusOK, contract.AccessTokenMetadata{Values: handler.metadata.TokenDefaults(id)})
	case http.MethodPut:
		var input contract.AccessTokenMetadata
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		if handler.accessTokens != nil {
			tokens, err := handler.accessTokens.List(request.Context())
			if err != nil {
				handler.writeAccessTokenError(writer, err)
				return
			}
			found := false
			for _, token := range tokens {
				found = found || token.ID == id
			}
			if !found {
				writeError(writer, http.StatusNotFound, "not_found", "access token not found")
				return
			}
		}
		for key, value := range input.Values {
			if !handler.metadata.Valid(key, value) {
				writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata_value", "the value of "+key+" does not suit the attribute")
				return
			}
		}
		if !handler.writeMetadataChange(writer, request.Context(), handler.metadataStore.ReplaceTokenMetadata(request.Context(), id, input.Values)) {
			return
		}
		writeJSON(writer, http.StatusOK, contract.AccessTokenMetadata{Values: handler.metadata.TokenDefaults(id)})
	default:
		writer.Header().Set("Allow", "GET, PUT")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use GET or PUT")
	}
}

// sessionMetadata serves a session's bindings under
// /control/v1/request-sessions/{id}/metadata. A binding set here goes in as
// manual. Removing one stores none instead of deleting it: with nothing
// stored, the session's next request would bind it again from the markers
// in the history its client replays.
func (handler *Handler) sessionMetadata(writer http.ResponseWriter, request *http.Request) {
	if !handler.metadataAvailable(writer) {
		return
	}
	rest := strings.TrimPrefix(request.URL.Path, RequestSessionsPath+"/")
	rawID, path, _ := strings.Cut(rest, "/metadata")
	id := contract.SessionID(rawID)
	key := strings.TrimPrefix(path, "/")
	if id.Validate() != nil || strings.Contains(key, "/") || (path != "" && key == "") {
		writeError(writer, http.StatusNotFound, "not_found", "control API path not found")
		return
	}
	detail, err := handler.requestRecords.GetRequestSession(request.Context(), string(id))
	if err != nil {
		handler.writeRequestRecordStoreError(writer, err)
		return
	}
	if key == "" {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "only GET is allowed")
			return
		}
		handler.writeSessionMetadata(writer, request.Context(), id)
		return
	}
	if _, found := handler.metadataAttribute(key); !found {
		writeError(writer, http.StatusNotFound, "not_found", "metadata attribute not found")
		return
	}
	value := contract.MetadataNone
	switch request.Method {
	case http.MethodPut:
		var input contract.SessionMetadataInput
		if !decodeControlJSON(writer, request, &input) {
			return
		}
		value = strings.TrimSpace(input.Value)
		if !handler.metadata.Valid(key, value) {
			writeError(writer, http.StatusUnprocessableEntity, "invalid_metadata_value", "the value of "+key+" does not suit the attribute")
			return
		}
	case http.MethodDelete:
	default:
		writer.Header().Set("Allow", "PUT, DELETE")
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed", "use PUT or DELETE")
		return
	}
	turn := detail.TurnCount
	for _, record := range detail.Turns {
		if record.TurnIndex != nil && *record.TurnIndex > turn {
			turn = *record.TurnIndex
		}
	}
	now := time.Now().UTC()
	if err := handler.metadataStore.PutMetadataValue(request.Context(), storage.MetadataValueRecord{
		TargetKind: storage.MetadataTargetSession, TargetID: string(id), Key: key, Value: value,
		SetBy: contract.MetadataSetByManual, Turn: turn, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		writeError(writer, http.StatusInternalServerError, "metadata_unavailable", "could not save the metadata")
		return
	}
	handler.writeSessionMetadata(writer, request.Context(), id)
}

func (handler *Handler) writeSessionMetadata(writer http.ResponseWriter, ctx context.Context, id contract.SessionID) {
	values, err := handler.metadataStore.MetadataValues(ctx, storage.MetadataTargetSession, string(id))
	if err != nil {
		writeError(writer, http.StatusInternalServerError, "metadata_unavailable", "could not read the metadata")
		return
	}
	response := contract.SessionMetadata{Values: map[string]contract.SessionMetadataValue{}}
	for key, value := range values {
		response.Values[key] = contract.SessionMetadataValue{Value: value.Value, SetBy: value.SetBy, Turn: value.Turn, UpdatedAt: value.UpdatedAt.UTC()}
	}
	writeJSON(writer, http.StatusOK, response)
}
