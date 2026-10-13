package cloud

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/QuantumNous/astrlink/core/internal/autoclassifier"
)

// jevRequest is the Jev Decisions call shape: one state string and a question
// map keyed by question name, whose criteria map is keyed by exact label.
type jevRequest struct {
	State     string                 `json:"state"`
	Model     string                 `json:"model"`
	Questions map[string]jevQuestion `json:"questions"`
}

type jevQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type jevResponse struct {
	Answers map[string]jevAnswer `json:"answers"`
}

type jevAnswer struct {
	Choice     *string        `json:"choice"`
	Confidence *float64       `json:"confidence"`
	Ranked     []rankedChoice `json:"ranked,omitempty"`
}

func buildJevRequest(in Input) (string, []byte, error) {
	request := jevRequest{
		State: in.Text,
		Model: in.Model,
		Questions: map[string]jevQuestion{
			"task_type": {
				Type:         "choice",
				Instructions: in.Prompt,
				Criteria:     labelDescriptions(in.Labels),
			},
		},
	}
	body, err := marshalRequestBody(request)
	if err != nil {
		return "", nil, err
	}
	return in.Endpoint, body, nil
}

func parseJevResponse(payload []byte, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	var response jevResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", 0, nil, errInvalidOutput
	}
	answer, ok := response.Answers["task_type"]
	if !ok {
		return "", 0, nil, errInvalidOutput
	}
	return validatedChoiceWithRanked(answer.Choice, answer.Confidence, answer.Ranked, labels)
}

// openAIDecisionsRequest is the OpenAI Decisions call shape: questions are an
// array in label order and answers align with that order.
type openAIDecisionsRequest struct {
	Model     string                    `json:"model"`
	Input     string                    `json:"input"`
	Questions []openAIDecisionsQuestion `json:"questions"`
}

type openAIDecisionsQuestion struct {
	Type         string                  `json:"type"`
	Name         string                  `json:"name"`
	Instructions string                  `json:"instructions"`
	Choices      []openAIDecisionsChoice `json:"choices"`
}

type openAIDecisionsChoice struct {
	Value       string `json:"value"`
	Description string `json:"description"`
}

type openAIDecisionsResponse struct {
	Answers []openAIDecisionsAnswer `json:"answers"`
}

type openAIDecisionsAnswer struct {
	Type       string         `json:"type"`
	Name       *string        `json:"name"`
	Choice     *string        `json:"choice"`
	Confidence *float64       `json:"confidence"`
	Ranked     []rankedChoice `json:"ranked,omitempty"`
}

func buildOpenAIDecisionsRequest(in Input) (string, []byte, error) {
	choices := make([]openAIDecisionsChoice, 0, len(in.Labels))
	for _, label := range in.Labels {
		choices = append(choices, openAIDecisionsChoice{Value: label, Description: label})
	}
	request := openAIDecisionsRequest{
		Model: in.Model,
		Input: in.Text,
		Questions: []openAIDecisionsQuestion{{
			Type:         "choice",
			Name:         "task_type",
			Instructions: in.Prompt,
			Choices:      choices,
		}},
	}
	body, err := marshalRequestBody(request)
	if err != nil {
		return "", nil, err
	}
	return in.Endpoint, body, nil
}

func parseOpenAIDecisionsResponse(payload []byte, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	var response openAIDecisionsResponse
	if err := json.Unmarshal(payload, &response); err != nil {
		return "", 0, nil, errInvalidOutput
	}
	// Prefer the answer that names the question; index alignment is the
	// fallback when the provider omits names.
	for _, answer := range response.Answers {
		if answer.Name != nil && *answer.Name != "task_type" {
			continue
		}
		return answerToChoice(answer, labels)
	}
	return "", 0, nil, errInvalidOutput
}

func answerToChoice(answer openAIDecisionsAnswer, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	if answer.Type == "refusal" {
		return "", 0, nil, errRefusal
	}
	return validatedChoiceWithRanked(answer.Choice, answer.Confidence, answer.Ranked, labels)
}

// rankedChoice is one entry of a provider's optional ranked answer list,
// carrying the same field names as the single choice above.
type rankedChoice struct {
	Choice     *string  `json:"choice"`
	Confidence *float64 `json:"confidence"`
}

// validatedChoiceWithRanked enforces the shared answer contract — a choice
// inside the effective label set and a finite confidence inside [0,1] — and
// additionally folds the provider's optional ranked list into the Outcome's
// candidate sequence. The primary choice stays the category. An absent, empty,
// or fully invalid ranked list leaves the candidates empty so callers fall
// back to the single-value form. A ranked list that survives normalization
// while omitting the primary is completed, not trusted: the primary is merged
// back in with its own score, so the presence of a ranked field can never
// displace a valid primary from the candidate sequence. When the ranked list
// already describes the primary, the ranked score is authoritative and the
// primary's own score does not overwrite it.
func validatedChoiceWithRanked(choice *string, confidence *float64, ranked []rankedChoice, labels []string) (string, float64, []autoclassifier.Candidate, error) {
	category, score, err := validatedChoice(choice, confidence, labels)
	if err != nil {
		return "", 0, nil, err
	}
	return category, score, candidatesWithPrimary(ranked, category, score, labels), nil
}

// candidatesWithPrimary layers the ranked list strictly on top of the
// single-value answer, never in place of it. A ranked list with no survivors
// (absent, empty, or fully invalid) still yields no candidates, preserving the
// single-value fallback. A surviving ranked list that omits the primary gets
// the primary merged in with its own score and the sequence re-sorted by the
// same rules normalizeRanked applies, so a valid-but-unranked primary still
// reaches selection. A ranked list that already describes the primary stands
// as-is — its score is kept, never overwritten by the primary's confidence.
// The low-confidence threshold stays out of this layer: a merged primary below
// threshold is filtered downstream in modelrouting.TaskTypeCandidateScores
// like any other entry.
func candidatesWithPrimary(entries []rankedChoice, category string, score float64, labels []string) []autoclassifier.Candidate {
	candidates := normalizeRanked(entries, labels)
	if len(candidates) == 0 {
		return nil
	}
	for _, candidate := range candidates {
		if candidate.Label == category {
			return candidates
		}
	}
	// The primary is a label absent from the sequence, so uniqueness and the
	// keeps-best-entry rule both hold after the merge; only the ordering has
	// to be redone.
	return sortRankedCandidates(append(candidates, autoclassifier.Candidate{Label: category, Score: score}), labels)
}

// normalizeRanked turns a provider's optional ranked list into the Outcome's
// candidate sequence, honoring the Outcome contract that the producing source
// sorts, deduplicates, and catalog-checks: entries outside the requested
// label set or with invalid scores drop, survivors sort by score descending
// with request order breaking ties, and each label keeps only its best entry.
// The low-confidence threshold is deliberately NOT applied here — threshold
// filtering belongs to the one conversion shared by the inference path and
// the control-plane dry-run (modelrouting.TaskTypeCandidateScores).
func normalizeRanked(entries []rankedChoice, labels []string) []autoclassifier.Candidate {
	if len(entries) == 0 {
		return nil
	}
	requestOrder := make(map[string]int, len(labels))
	for index, label := range labels {
		requestOrder[label] = index
	}
	best := make(map[string]float64, len(labels))
	for _, entry := range entries {
		if entry.Choice == nil || *entry.Choice == "" || entry.Confidence == nil {
			continue
		}
		if _, ok := requestOrder[*entry.Choice]; !ok {
			continue
		}
		if math.IsNaN(*entry.Confidence) || math.IsInf(*entry.Confidence, 0) || *entry.Confidence < 0 || *entry.Confidence > 1 {
			continue
		}
		if existing, ok := best[*entry.Choice]; !ok || *entry.Confidence > existing {
			best[*entry.Choice] = *entry.Confidence
		}
	}
	if len(best) == 0 {
		return nil
	}
	candidates := make([]autoclassifier.Candidate, 0, len(best))
	for label, score := range best {
		candidates = append(candidates, autoclassifier.Candidate{Label: label, Score: score})
	}
	return sortRankedCandidates(candidates, labels)
}

// sortRankedCandidates applies the Outcome's ordering contract: score
// descending, request order breaking ties. Both normalizeRanked's survivors
// and a primary merged into an already-normalized sequence go through this
// one comparator so the ordering rules cannot drift apart.
func sortRankedCandidates(candidates []autoclassifier.Candidate, labels []string) []autoclassifier.Candidate {
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
	return candidates
}

// validatedChoice enforces the shared answer contract: a choice inside the
// effective label set and a finite confidence inside [0,1]. Anything else is
// invalid output, which the caller records as a deterministic fallback.
func validatedChoice(choice *string, confidence *float64, labels []string) (string, float64, error) {
	if choice == nil || *choice == "" {
		return "", 0, errInvalidOutput
	}
	if confidence == nil || math.IsNaN(*confidence) || math.IsInf(*confidence, 0) || *confidence < 0 || *confidence > 1 {
		return "", 0, errInvalidOutput
	}
	for _, label := range labels {
		if label == *choice {
			return *choice, *confidence, nil
		}
	}
	return "", 0, errInvalidOutput
}

var errInvalidOutput = fmt.Errorf("classifier answer is not a valid choice")
