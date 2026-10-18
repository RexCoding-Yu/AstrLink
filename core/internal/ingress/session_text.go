package ingress

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/QuantumNous/astrlink/core/contract"
)

var (
	previewURLPattern    = regexp.MustCompile(`https?://\S+`)
	previewSecretPattern = regexp.MustCompile(`(?i)\b(sk-[A-Za-z0-9_-]{8,}|bearer\s+[A-Za-z0-9._\-+/=]{8,}|api[_-]?key\s*[:=]\s*\S+)`)
	placeholderPattern   = regexp.MustCompile(`<PRIVATE_[A-Z0-9_]+>`)
	// previewNoisePattern is leading markdown that carries no words: heading
	// marks, block quotes, and bullets ("-" only when it is a bullet).
	previewNoisePattern = regexp.MustCompile(`^(?:[#>]+\s*|[*•·]\s*|-\s+)+`)
	// previewRolePattern is a transcript-style speaker label a client
	// prepends to the text: "[User]: …", "Human: …", "用户：…".
	previewRolePattern = regexp.MustCompile(`^(?:\[[^\[\]\n]{1,16}\]|(?i:user|human|assistant|system)|用户|人类)\s*[:：]\s*`)
)

// previewTargetRunes is where a preview is cut when the first line runs on.
// It leaves room for the ellipsis inside contract.MaxInputPreviewRunes.
const previewTargetRunes = 64

// previewMinSentenceRunes stops a sentence cut from producing a stub like
// "好。" when the first sentence is that short.
const previewMinSentenceRunes = 12

// filePathExtensionLimit bounds the extension that marks a path segment as a
// file name, so a long word after a dot is not taken for one.
const filePathExtensionLimit = 10

// filePathSegmentBreaks end a path segment besides whitespace and separators:
// shell and markup delimiters, and the punctuation of CJK prose.
const filePathSegmentBreaks = ":*?\"'`<>|,;，。？！：；、（）【】「」『』《》“”‘’"

// extractProtocolCursor reads a string field, or the id of an object field,
// as a bounded cursor. Conversation-level cursors (conversation, metadata,
// prompt_cache_key, container, cachedContent) are extracted by the convo
// adapters; this only serves the official previous_response_id chain.
func extractProtocolCursor(fields map[string]json.RawMessage, key string) string {
	raw, ok := fields[key]
	if !ok {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return clampCursor(strings.TrimSpace(value))
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return ""
	}
	idRaw, ok := object["id"]
	if !ok {
		return ""
	}
	if json.Unmarshal(idRaw, &value) != nil {
		return ""
	}
	return clampCursor(strings.TrimSpace(value))
}

// sanitizePreview shapes the newest visible user text into a title: secrets
// and URLs are redacted, the first line that still has words is taken with
// its leading markup and speaker label removed and its file paths shortened
// to file names, and a long line is cut at the end of its first sentence or,
// failing that, at a word or punctuation boundary with an ellipsis.
// Structure only — no phrase lists — so it holds across clients and
// languages.
func sanitizePreview(raw string) string {
	cleaned := previewURLPattern.ReplaceAllString(raw, "…")
	cleaned = previewSecretPattern.ReplaceAllString(cleaned, "…")
	cleaned = placeholderPattern.ReplaceAllString(cleaned, "…")
	if !utf8.ValidString(cleaned) {
		return ""
	}
	line := shortenFilePaths(firstPreviewLine(cleaned))
	if line == "" {
		return ""
	}
	if sentence, ok := cutPreviewSentence(line); ok {
		return contract.ClampRunes(sentence, contract.MaxInputPreviewRunes)
	}
	return contract.ClampRunes(truncatePreview(line, previewTargetRunes), contract.MaxInputPreviewRunes)
}

// maskProtected replaces every occurrence of what a privacy decision
// protected with the ellipsis sanitizePreview uses for secrets. The replacer
// tries values in argument order, so the longest go first: a value inside a
// longer one must not leave the longer one's remainder in the clear.
func maskProtected(text string, values []string) string {
	ordered := slices.Clone(values)
	slices.SortStableFunc(ordered, func(left, right string) int {
		return len(right) - len(left)
	})
	pairs := make([]string, 0, 2*len(ordered))
	for _, value := range ordered {
		if value != "" {
			pairs = append(pairs, value, "…")
		}
	}
	if len(pairs) == 0 {
		return text
	}
	return strings.NewReplacer(pairs...).Replace(text)
}

// firstPreviewLine returns the first line that keeps words after markup and
// speaker labels are removed, with inner whitespace collapsed. Lines without
// a letter or digit (rules, fences, stray punctuation) are skipped.
func firstPreviewLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		line = previewNoisePattern.ReplaceAllString(line, "")
		line = previewRolePattern.ReplaceAllString(line, "")
		line = strings.Join(strings.Fields(line), " ")
		if strings.ContainsFunc(line, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			return line
		}
	}
	return ""
}

// shortenFilePaths replaces each absolute file path in line with its file
// name. A terminal pastes a dropped file as its full path, often glued to the
// question after it, and the directories would fill the title. Only a path
// ending in a name with an extension counts, so directories, API routes,
// slash commands and relative paths stay. Keep the rules in step with the
// desktop's file-path-spans.ts, which splits the user message the same way.
func shortenFilePaths(line string) string {
	if !strings.ContainsAny(line, `/\`) {
		return line
	}
	runes := []rune(line)
	var out strings.Builder
	plain := 0
	for index := 0; index < len(runes); {
		if index > 0 && blocksFilePath(runes[index-1]) {
			index++
			continue
		}
		end, name, ok := filePathAt(runes, index)
		if !ok {
			index++
			continue
		}
		out.WriteString(string(runes[plain:index]))
		out.WriteString(name)
		plain, index = end, end
	}
	if plain == 0 {
		return line
	}
	out.WriteString(string(runes[plain:]))
	return out.String()
}

// filePathAt reads the file path that starts at runes[start]: `/dir/name.ext`,
// `~/name.ext` or `C:\dir\name.ext`. It returns where the path ends and the
// file name, unescaped where a shell escaped it.
func filePathAt(runes []rune, start int) (int, string, bool) {
	windows := start+2 < len(runes) && isASCIILetter(runes[start]) && runes[start+1] == ':' &&
		(runes[start+2] == '\\' || runes[start+2] == '/')
	index, minSegments := 0, 1
	switch {
	case windows:
		index = start + 3
	case runes[start] == '~' && start+1 < len(runes) && runes[start+1] == '/':
		index = start + 2
	case runes[start] == '/':
		// A root path needs a directory, so `/review` stays a command.
		index, minSegments = start+1, 2
	default:
		return 0, "", false
	}

	segments, segmentStart, segmentEnd := 0, index, index
	for {
		cursor, glued := index, false
		for cursor < len(runes) {
			r := runes[cursor]
			if !windows && r == '\\' && cursor+1 < len(runes) && runes[cursor+1] != '\n' {
				cursor += 2
				continue
			}
			if unicode.IsSpace(r) || r == '/' || r == '\\' || strings.ContainsRune(filePathSegmentBreaks, r) {
				break
			}
			// A file name glued to CJK prose ends at its extension.
			if r >= utf8.RuneSelf && extensionEndsAt(runes, index, cursor) {
				glued = true
				break
			}
			cursor++
		}
		if cursor == index {
			break
		}
		segments++
		segmentStart, segmentEnd = index, cursor
		if glued || cursor == len(runes) || (runes[cursor] != '/' && !(windows && runes[cursor] == '\\')) {
			break
		}
		index = cursor + 1
	}
	if segments < minSegments {
		return 0, "", false
	}

	// Trailing text such as `:12`, `#L4` or a full stop is not the file's.
	end := segmentEnd
	for end > segmentStart &&
		!(extensionEndsAt(runes, segmentStart, end) && (end == segmentEnd || !isASCIIAlnum(runes[end]))) {
		end--
	}
	if end == segmentStart {
		return 0, "", false
	}
	name := runes[segmentStart:end]
	if windows {
		return end, string(name), true
	}
	unescaped := make([]rune, 0, len(name))
	for i := 0; i < len(name); i++ {
		if name[i] == '\\' && i+1 < len(name) {
			i++
		}
		unescaped = append(unescaped, name[i])
	}
	return end, string(unescaped), true
}

// extensionEndsAt reports whether runes[start:end] ends in `.ext` after a
// non-empty name.
func extensionEndsAt(runes []rune, start, end int) bool {
	dot, letter := end-1, false
	for dot > start && isASCIIAlnum(runes[dot]) {
		letter = letter || isASCIILetter(runes[dot])
		dot--
	}
	length := end - 1 - dot
	return dot > start && runes[dot] == '.' && letter && length >= 1 && length <= filePathExtensionLimit
}

// blocksFilePath reports whether a path right after r would be relative or
// part of a URL.
func blocksFilePath(r rune) bool {
	return isASCIIAlnum(r) || strings.ContainsRune(`_.~/\-`, r)
}

func isASCIILetter(r rune) bool {
	return ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
}

func isASCIIAlnum(r rune) bool {
	return isASCIILetter(r) || ('0' <= r && r <= '9')
}

// cutPreviewSentence returns the first sentence of a line that would
// otherwise be truncated, when that sentence is long enough to stand alone
// and short enough to fit the contract bound whole. A closing question or
// exclamation mark is kept; a full stop is dropped. ok is false when the
// line is short enough already or no such sentence end exists.
func cutPreviewSentence(line string) (string, bool) {
	runes := []rune(line)
	if len(runes) <= previewTargetRunes {
		return "", false
	}
	for index := previewMinSentenceRunes; index < len(runes) && index < contract.MaxInputPreviewRunes; index++ {
		switch runes[index] {
		case '。':
			return string(runes[:index]), true
		case '！', '？':
			return string(runes[:index+1]), true
		case '.', '!', '?':
			if index+1 < len(runes) && !unicode.IsSpace(runes[index+1]) {
				continue
			}
			if runes[index] == '.' {
				return string(runes[:index]), true
			}
			return string(runes[:index+1]), true
		}
	}
	return "", false
}

// truncatePreview cuts line to at most limit runes plus an ellipsis, backing
// up to the nearest whitespace or punctuation within the last quarter of the
// budget so words and clauses are not split; CJK text without either is cut
// at the limit.
func truncatePreview(line string, limit int) string {
	runes := []rune(line)
	if len(runes) <= limit {
		return line
	}
	cut := limit
	for index := limit; index > limit-limit/4; index-- {
		if unicode.IsSpace(runes[index]) || unicode.IsPunct(runes[index]) {
			cut = index
			break
		}
	}
	return strings.TrimRightFunc(string(runes[:cut]), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	}) + "…"
}

func sanitizeSummary(raw string) string {
	// Transport error text may keep host/URL (ADR 0016); only credentials
	// and placeholder originals are stripped.
	cleaned := redactTransportSecrets(raw)
	return contract.ClampRunes(cleaned, contract.MaxEventSummaryRunes)
}

func clampCursor(value string) string {
	if value == "" {
		return ""
	}
	value = contract.ClampRunes(value, contract.MaxProtocolCursorRunes)
	if strings.ContainsAny(value, "\x00\n\r") {
		return ""
	}
	return value
}
