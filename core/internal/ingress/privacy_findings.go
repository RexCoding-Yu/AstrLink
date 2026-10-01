package ingress

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
)

// privacyFindingKeyPattern admits schema-like object keys. Any other key is
// caller-chosen (a tool argument named after an address, say) and is shown
// as "*" so a finding path never repeats request content.
var privacyFindingKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

// mergePrivacyFindings counts accepted findings by kind and structural path.
// Paths are JSON pointers from the inspection segments; values never enter.
func mergePrivacyFindings(existing []contract.PrivacyFinding, findings []privacy.Finding) []contract.PrivacyFinding {
	for _, finding := range findings {
		kind := contract.CanonicalKind(finding.Kind)
		if !kind.Valid() {
			continue
		}
		path := privacyFindingPath(finding.Path)
		merged := false
		for index := range existing {
			if existing[index].Kind == kind && existing[index].JSONPath == path {
				existing[index].Count++
				merged = true
				break
			}
		}
		if merged {
			continue
		}
		if len(existing) >= contract.MaxPrivacyFindings {
			continue
		}
		existing = append(existing, contract.PrivacyFinding{Kind: kind, JSONPath: path, Count: 1})
	}
	return existing
}

func privacyFindingPath(pointer string) string {
	tokens := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var builder strings.Builder
	for _, token := range tokens {
		if token == "" {
			continue
		}
		builder.WriteByte('/')
		if isPrivacyFindingIndex(token) || privacyFindingKeyPattern.MatchString(token) {
			builder.WriteString(token)
		} else {
			builder.WriteByte('*')
		}
	}
	path := builder.String()
	if path == "" {
		return "/"
	}
	path = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, path)
	if runes := []rune(path); len(runes) > contract.MaxPrivacyFindingPathRunes {
		path = string(runes[:contract.MaxPrivacyFindingPathRunes])
	}
	return path
}

func isPrivacyFindingIndex(token string) bool {
	if token == "" || len(token) > 9 {
		return false
	}
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
