package contract

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// VertexConnection identifies the project charged for requests and their location.
type VertexConnection struct {
	ProjectID string `json:"project_id"`
	Location  string `json:"location"`
}

var vertexProjectPattern = regexp.MustCompile(`^(?:(?:[a-z0-9.-]+:)?[a-z][a-z0-9-]{4,28}[a-z0-9]|[0-9]{1,20})$`)
var vertexLocationPattern = regexp.MustCompile(`^(?:global|us|eu|[a-z]+-[a-z]+[0-9]+)$`)

func (connection VertexConnection) Validate() error {
	if len(connection.ProjectID) > 254 || !vertexProjectPattern.MatchString(connection.ProjectID) {
		return fmt.Errorf("vertex.project_id must be a Google Cloud project ID or number")
	}
	if len(connection.Location) > 64 || !vertexLocationPattern.MatchString(connection.Location) {
		return fmt.Errorf("vertex.location must be global, us, eu, or a Google Cloud region")
	}
	return nil
}

func (connection VertexConnection) BaseURL() string {
	switch connection.Location {
	case "global":
		return "https://aiplatform.googleapis.com"
	case "us", "eu":
		return "https://aiplatform." + connection.Location + ".rep.googleapis.com"
	default:
		return "https://" + connection.Location + "-aiplatform.googleapis.com"
	}
}

// VertexLocation infers the location from a regional API hostname.
// Other hosts, including reverse proxies, use global.
func VertexLocation(baseURL string) string {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "global"
	}
	host := strings.ToLower(base.Hostname())
	for _, location := range []string{"us", "eu"} {
		if host == "aiplatform."+location+".rep.googleapis.com" {
			return location
		}
	}
	if location, ok := strings.CutSuffix(host, "-aiplatform.googleapis.com"); ok && vertexLocationPattern.MatchString(location) {
		return location
	}
	return "global"
}

func (service Service) VertexExpress() bool {
	return service.Kind == ServiceKindVertex && service.HTTP != nil && service.HTTP.Auth.Scheme == AuthSchemeGoogleAPIKey
}

func (connection *VertexConnection) UnmarshalJSON(data []byte) error {
	type document VertexConnection
	var value document
	if err := decodePolicy(data, &value, []string{"project_id", "location"}, nil); err != nil {
		return err
	}
	*connection = VertexConnection(value)
	return nil
}
