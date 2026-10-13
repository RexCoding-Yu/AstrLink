package modelrouting

import (
	"sort"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
)

// TaskTypeOutcome is one classification attempt's contribution to routing
// facts. Candidates is the classifier's ranked output (or nil for
// single-value sources); Value and Candidates may both be set — structured
// sources report the primary label and the ranked list on separate fields —
// and the conversion below reconciles the two instead of trusting one. The
// conversion into Facts.TaskTypeCandidates happens in
// TaskTypeCandidateLabels, the single place that orders, filters, and
// deduplicates. The inference path (ingress) and the control-plane dry-run
// (controlapi) both build this carrier, so both run through the same
// conversion below instead of each deriving its own candidate sequence.
type TaskTypeOutcome struct {
	Value                  string
	Code                   string
	DurationMS             int
	ClassifierID           string
	Confidence             float64
	Candidates             []autoclassifier.Candidate
	LowConfidenceThreshold float64
}

// TaskTypeCandidateScores is the one Outcome→sequence conversion both fact
// building and observation building consume (W5/W6): it turns the
// classifier's ranked output into the ordered candidate sequence the
// evaluator substitutes, keeping each label's score. Ranked candidates are
// threshold-filtered and label-checked; single-value sources contribute
// their one confident label; fallback paths (any recorded code, including
// fallback=general) contribute nothing, because a fallback is not a
// measurement. A ranked sequence that omits the primary answer is completed,
// not trusted: the primary merges in with its own score before the shared
// filters run, so the presence of a ranked field can never displace a valid
// primary from selection; when the merged primary survives, the sequence
// re-sorts by score descending with the group's effective label order
// breaking ties, keeping ranks consistent with the substituted sequence. A
// primary already inside the sequence stands as-is — the merge is
// idempotent. Scores ride along for the observation side only — facts stay
// label-only so scores can never re-enter selection.
func TaskTypeCandidateScores(outcome TaskTypeOutcome, group contract.ModelRoutingGroup) []autoclassifier.Candidate {
	if outcome.Code != "" {
		return nil
	}
	candidates := outcome.Candidates
	mergedPrimary := false
	if len(candidates) == 0 {
		if outcome.Value == "" {
			return nil
		}
		candidates = []autoclassifier.Candidate{{Label: outcome.Value, Score: outcome.Confidence}}
	} else if outcome.Value != "" && !taskTypeCandidatesContainLabel(candidates, outcome.Value) {
		// The primary is a label absent from the sequence, so uniqueness and
		// the keeps-first-entry rule both hold after the merge. Copy before
		// appending: the producer owns the original slice.
		merged := make([]autoclassifier.Candidate, 0, len(candidates)+1)
		merged = append(merged, candidates...)
		candidates = append(merged, autoclassifier.Candidate{Label: outcome.Value, Score: outcome.Confidence})
		mergedPrimary = true
	}
	scored := make([]autoclassifier.Candidate, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate.Score < outcome.LowConfidenceThreshold {
			continue
		}
		if !group.ValidTaskTypeLabel(candidate.Label) {
			continue
		}
		if _, ok := seen[candidate.Label]; ok {
			continue
		}
		seen[candidate.Label] = struct{}{}
		scored = append(scored, candidate)
	}
	if mergedPrimary {
		if _, ok := seen[outcome.Value]; ok {
			sortTaskTypeCandidates(scored, group)
		}
	}
	return scored
}

// taskTypeCandidatesContainLabel reports whether the ranked sequence already
// describes the label.
func taskTypeCandidatesContainLabel(candidates []autoclassifier.Candidate, label string) bool {
	for _, candidate := range candidates {
		if candidate.Label == label {
			return true
		}
	}
	return false
}

// sortTaskTypeCandidates applies the merged-sequence ordering contract:
// score descending, the group's effective label order breaking ties. That
// order is exactly what the classification request listed (the call plan
// resolves its labels from the same group), so it matches the comparator the
// producing sources run (autoclassifier/cloud's sortRankedCandidates) without
// importing it: that one is package-private and keyed by the per-request
// label slice, while this one derives the key from the group's catalog.
func sortTaskTypeCandidates(candidates []autoclassifier.Candidate, group contract.ModelRoutingGroup) {
	labels := contract.EffectiveModelRoutingTaskTypeLabels(group.TaskTypeLabels)
	requestOrder := make(map[string]int, len(labels))
	for index, label := range labels {
		requestOrder[label] = index
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return requestOrder[candidates[i].Label] < requestOrder[candidates[j].Label]
	})
}

// TaskTypeCandidateLabels feeds the evaluator: the filtered sequence from
// TaskTypeCandidateScores reduced to ordered labels only.
func TaskTypeCandidateLabels(outcome TaskTypeOutcome, group contract.ModelRoutingGroup) []string {
	scored := TaskTypeCandidateScores(outcome, group)
	if len(scored) == 0 {
		return nil
	}
	labels := make([]string, 0, len(scored))
	for _, candidate := range scored {
		labels = append(labels, candidate.Label)
	}
	return labels
}

// TaskTypeCandidateObservations renders the filtered candidate sequence into
// the persisted observation shape. Rank is the 0-based position in the very
// sequence the evaluator substituted, so it always lines up with
// Selection.TaskTypeCandidateIndex; adopted (nil when no candidate was
// substituted) marks at most that one entry as chosen. A field that never
// went through scored fallback gets no candidates at all, never a list of
// all-false rows.
func TaskTypeCandidateObservations(outcome TaskTypeOutcome, group contract.ModelRoutingGroup, adopted *int) []contract.ModelRoutingCandidateObservation {
	scored := TaskTypeCandidateScores(outcome, group)
	if len(scored) == 0 {
		return nil
	}
	observations := make([]contract.ModelRoutingCandidateObservation, 0, len(scored))
	for index, candidate := range scored {
		observations = append(observations, contract.ModelRoutingCandidateObservation{
			Value:  candidate.Label,
			Score:  candidate.Score,
			Rank:   index,
			Chosen: adopted != nil && *adopted == index,
		})
	}
	return observations
}
