package classifieroutput

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
)

// ErrInvalidSchema marks output that decoded as strict JSON but does not
// satisfy the task-type schema: unknown keys, missing label, labels outside
// the calling catalog, or non-finite scores.
var ErrInvalidSchema = errors.New("classifier output fails the task-type schema")

// Candidate is one ranked label from the classifier output.
type Candidate struct {
	Label string
	Score float64
}

// TaskType is the validated classification answer: the adopted top label,
// its upstream-reported score, and the full ranked candidate sequence after
// normalization.
type TaskType struct {
	Label      string
	Score      float64
	Candidates []Candidate
}

// ParseTaskType runs layers 2 and 3 over one assistant text. labels is the
// immutable catalog snapshot of the group being routed; anything outside it
// is rejected, never coerced. A missing score stays zero rather than being
// invented; a missing candidates list stays empty. Candidates are sorted by
// score descending with catalog order breaking ties, deduplicated by label,
// and capped at the catalog size.
func ParseTaskType(text string, labels []string) (TaskType, error) {
	catalog := make(map[string]int, len(labels))
	for index, label := range labels {
		catalog[label] = index
	}
	object, err := ParseObject(text)
	if err != nil {
		return TaskType{}, err
	}
	for key := range object {
		switch key {
		case "label", "score", "candidates":
		default:
			return TaskType{}, fmt.Errorf("%w: unknown key %q", ErrInvalidSchema, key)
		}
	}
	var result TaskType
	if raw, ok := object["label"]; ok {
		if err := json.Unmarshal(raw, &result.Label); err != nil {
			return TaskType{}, fmt.Errorf("%w: label must be a string", ErrInvalidSchema)
		}
		if _, ok := catalog[result.Label]; !ok {
			return TaskType{}, fmt.Errorf("%w: label %q is outside the catalog", ErrInvalidSchema, result.Label)
		}
	}
	if raw, ok := object["score"]; ok {
		if err := json.Unmarshal(raw, &result.Score); err != nil {
			return TaskType{}, fmt.Errorf("%w: score must be a number", ErrInvalidSchema)
		}
		if !validScore(result.Score) {
			return TaskType{}, fmt.Errorf("%w: score must be finite and within [0,1]", ErrInvalidSchema)
		}
	}
	if raw, ok := object["candidates"]; ok {
		var entries []struct {
			Label string  `json:"label"`
			Score float64 `json:"score"`
		}
		if err := json.Unmarshal(raw, &entries); err != nil {
			return TaskType{}, fmt.Errorf("%w: candidates must be an array of objects", ErrInvalidSchema)
		}
		if len(entries) > MaxCandidates {
			return TaskType{}, fmt.Errorf("%w: candidates exceed %d entries", ErrInvalidSchema, MaxCandidates)
		}
		for _, entry := range entries {
			if _, ok := catalog[entry.Label]; !ok {
				return TaskType{}, fmt.Errorf("%w: candidate %q is outside the catalog", ErrInvalidSchema, entry.Label)
			}
			if !validScore(entry.Score) {
				return TaskType{}, fmt.Errorf("%w: candidate %q score must be finite and within [0,1]", ErrInvalidSchema, entry.Label)
			}
			result.Candidates = append(result.Candidates, Candidate{Label: entry.Label, Score: entry.Score})
		}
	}
	if result.Label == "" && len(result.Candidates) == 0 {
		return TaskType{}, fmt.Errorf("%w: neither label nor candidates present", ErrInvalidSchema)
	}
	result.Candidates = normalizeCandidates(result.Candidates, catalog, len(labels))
	if result.Label == "" {
		// A candidates-only answer adopts its normalized top entry.
		result.Label = result.Candidates[0].Label
		result.Score = result.Candidates[0].Score
	}
	return result, nil
}

func validScore(score float64) bool {
	return !math.IsNaN(score) && !math.IsInf(score, 0) && score >= 0 && score <= 1
}

// normalizeCandidates sorts by score descending with catalog order breaking
// ties, drops duplicate labels keeping the highest score, and caps the list
// at the catalog size.
func normalizeCandidates(candidates []Candidate, catalog map[string]int, size int) []Candidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return catalog[candidates[i].Label] < catalog[candidates[j].Label]
	})
	normalized := make([]Candidate, 0, min(len(candidates), size))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate.Label]; ok {
			continue
		}
		seen[candidate.Label] = struct{}{}
		normalized = append(normalized, candidate)
		if len(normalized) == size {
			break
		}
	}
	return normalized
}
