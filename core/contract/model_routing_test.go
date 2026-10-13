package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestModelRoutingGroupContractAndRedirectConflicts(t *testing.T) {
	var group ModelRoutingGroup
	if err := json.Unmarshal([]byte(`{"id":"superclaude","enabled":true,"members":[{"id":"fast","service_id":"anthropic","model":"claude-haiku"},{"id":"strong","service_id":"anthropic","model":"claude-sonnet"}],"default_member_id":"fast","rules":[{"id":"coding","priority":100,"when":{"all":[{"field":"task_type","in":["coding"]}]},"target_member_id":"strong"}]}`), &group); err != nil {
		t.Fatal(err)
	}
	if err := ValidateModelRoutingGroups([]ModelRoutingGroup{group}, []ModelRedirect{{From: "claude-sonnet", To: "other", Enabled: false}}); err == nil {
		t.Fatal("expected disabled redirect conflict")
	}
	if err := json.Unmarshal([]byte(`{"id":"x","enabled":true,"members":[{"id":"a","service_id":"service_01","model":"m"}],"default_member_id":"a","rules":[{"id":"r","priority":1,"when":{"field":"domains","in":["x"]},"target_member_id":"a"}]}`), &group); err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err == nil || !strings.Contains(err.Error(), "unsupported_routing_field") {
		t.Fatalf("domains error = %v", err)
	}
}

func TestModelRoutingConditionRejectsDuplicateKeys(t *testing.T) {
	var group ModelRoutingGroup
	err := json.Unmarshal([]byte(`{"id":"x","enabled":true,"members":[{"id":"a","service_id":"s","model":"m"}],"default_member_id":"a","rules":[],"rules":[]}`), &group)
	if err == nil || !strings.Contains(err.Error(), "duplicate JSON key") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestModelRoutingSnapshotVersionIsOrderIndependent(t *testing.T) {
	groups := []ModelRoutingGroup{{ID: "x", Enabled: true, Members: []ModelRoutingMember{{ID: "b", ServiceID: "service_01", Model: "b"}, {ID: "a", ServiceID: "service_01", Model: "a"}}, DefaultMemberID: "a", Rules: []ModelRoutingRule{{ID: "z", Priority: 2, When: ModelRoutingCondition{Field: "protocol", Equals: "openai_chat"}, TargetMemberID: "a"}, {ID: "a", Priority: 1, When: ModelRoutingCondition{Field: "protocol", Equals: "openai_chat"}, TargetMemberID: "b"}}}}
	first, err := ModelRoutingSnapshotVersion(groups)
	if err != nil {
		t.Fatal(err)
	}
	groups[0].Members[0], groups[0].Members[1] = groups[0].Members[1], groups[0].Members[0]
	groups[0].Rules[0], groups[0].Rules[1] = groups[0].Rules[1], groups[0].Rules[0]
	second, err := ModelRoutingSnapshotVersion(groups)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("versions differ: %s != %s", first, second)
	}
}

func TestModelRoutingDecisionWireGoldenSample(t *testing.T) {
	priority := 100
	decision := ModelRoutingDecision{
		SchemaVersion:         1,
		VirtualModelID:        "astrlink/superclaude",
		GroupID:               "superclaude",
		ConfigSnapshotVersion: strings.Repeat("a", 64),
		SelectionSource:       ModelRoutingSelectionRule,
		RuleID:                "coding",
		RulePriority:          &priority,
		UsedDefault:           false,
		Fields: []ModelRoutingFieldObservation{{
			Field:        "task_type",
			Source:       "classifier",
			Known:        true,
			MatchCount:   1,
			ClassifierID: "classifier_ab0123456789abcdef0123456789abcd",
		}},
		MemberID:        "strong",
		MemberServiceID: "service_01",
		MemberModel:     "claude-sonnet",
		Execution: ModelRoutingExecution{
			Status:       ModelRoutingExecutionSucceeded,
			AttemptCount: 1,
		},
		PrivacyAttempts: []ModelRoutingPrivacyAttempt{{
			AttemptIndex: 0,
			ServiceID:    "service_01",
			Decision:     ModelRoutingPrivacyAllow,
			FindingCount: 0,
			DurationMS:   2,
		}},
		DurationMS:           23,
		ClassifierDurationMS: 7,
	}
	if err := decision.Validate(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.MarshalIndent(decision, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip ModelRoutingDecision
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("golden sample does not round-trip: %v", err)
	}
	if outputPath := os.Getenv("ASTRLINK_MODEL_ROUTING_GOLDEN_SAMPLE"); outputPath != "" {
		if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(outputPath, append(encoded, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("Go contract wire sample:\n%s", encoded)
}

func TestRoutingSettingsValidateDesktopModelRoutingGroupShape(t *testing.T) {
	var groups []ModelRoutingGroup
	if err := json.Unmarshal([]byte(`[{
		"id":"superclaude",
		"enabled":true,
		"members":[
			{"id":"fast","service_id":"service_01","model":"claude-haiku"},
			{"id":"strong","service_id":"service_01","model":"claude-sonnet"}
		],
		"default_member_id":"fast",
		"rules":[{
			"id":"coding-streaming",
			"priority":100,
			"when":{"all":[
				{"field":"task_type","in":["coding"]},
				{"field":"streaming","equals_bool":true}
			]},
			"target_member_id":"strong"
		}]
	}]`), &groups); err != nil {
		t.Fatal(err)
	}
	settings := DefaultRoutingSettings()
	settings.ModelRoutingGroups = groups
	if err := settings.Validate(); err != nil {
		t.Fatalf("desktop model_routing_groups shape rejected: %v", err)
	}
	if err := ValidateModelRoutingGroups(settings.ModelRoutingGroups, settings.ModelRedirects); err != nil {
		t.Fatalf("desktop model_routing_groups shape failed group validation: %v", err)
	}
}

// routingConditionError decodes one group whose single rule carries `when` and
// returns the decode or validation error, or nil when the condition is accepted.
func routingConditionError(t *testing.T, when string) error {
	t.Helper()
	document := `{"id":"g","enabled":true,"members":[{"id":"a","service_id":"service_01","model":"m"}],"default_member_id":"a","rules":[{"id":"r","priority":1,"when":` + when + `,"target_member_id":"a"}]}`
	var group ModelRoutingGroup
	if err := json.Unmarshal([]byte(document), &group); err != nil {
		return err
	}
	return group.Validate()
}

func TestModelRoutingConditionAcceptsExtendedOperators(t *testing.T) {
	for _, when := range []string{
		// String kind: in / not_in / equals / not_equals.
		`{"field":"protocol","not_in":["openai_chat","anthropic_messages"]}`,
		`{"field":"protocol","not_equals":"openai_chat"}`,
		`{"field":"task_type","not_in":["research","architect"]}`,
		`{"field":"task_type","not_equals":"general"}`,
		`{"field":"media_kind","not_equals":"none"}`,
		`{"field":"language","not_in":["en","zh-cn"]}`,
		`{"field":"language","not_equals":"en"}`,
		// Number kind: the new bounded operators, single and paired.
		`{"field":"turn_count","gt":3}`,
		`{"field":"turn_count","gte":3}`,
		`{"field":"turn_count","lt":3}`,
		`{"field":"turn_count","lte":3}`,
		`{"field":"input_bytes","gt":0,"lt":1024}`,
		`{"field":"input_bytes","gte":5,"lte":10}`,
		`{"field":"input_bytes","gte":5,"lt":10}`,
		`{"field":"input_bytes","gt":5,"lte":10}`,
		`{"field":"input_bytes","gte":0,"lte":0}`,
		// An exclusive lower bound equal to an inclusive upper bound is an
		// empty range: the ordering holds, so the leaf is well-formed.
		`{"field":"turn_count","gt":5,"lte":5}`,
		// Number kind: the legacy bounds keep working beside the new ones.
		`{"field":"turn_count","min":2}`,
		`{"field":"turn_count","max":2}`,
		`{"field":"turn_count","min":2,"max":4}`,
		`{"field":"turn_count","min":2,"lte":4}`,
		`{"field":"turn_count","gte":2,"max":4}`,
		// Keyword kind: contains_any / not_contains_any.
		`{"field":"literal_keyword","contains_any":["x"]}`,
		`{"field":"literal_keyword","not_contains_any":["x","y"]}`,
		// The additions compose inside a tree.
		`{"all":[{"field":"protocol","not_equals":"openai_chat"},{"field":"turn_count","gte":2,"lte":8}]}`,
	} {
		if err := routingConditionError(t, when); err != nil {
			t.Fatalf("%s rejected: %v", when, err)
		}
	}
}

func TestModelRoutingConditionRejectsOperatorForWrongFieldKind(t *testing.T) {
	for _, when := range []string{
		// A string field refuses the numeric and keyword operators.
		`{"field":"protocol","gt":1}`,
		`{"field":"protocol","gte":1}`,
		`{"field":"protocol","lt":1}`,
		`{"field":"protocol","lte":1}`,
		`{"field":"protocol","not_contains_any":["x"]}`,
		// A bool field stays equals_bool-only.
		`{"field":"streaming","not_in":["true"]}`,
		`{"field":"streaming","not_equals":"true"}`,
		`{"field":"streaming","gt":1}`,
		`{"field":"streaming","not_contains_any":["x"]}`,
		// A number field refuses the string and keyword operators.
		`{"field":"turn_count","not_in":["1"]}`,
		`{"field":"turn_count","not_equals":"1"}`,
		`{"field":"turn_count","not_contains_any":["x"]}`,
		// A keyword field takes only contains_any or not_contains_any.
		`{"field":"literal_keyword","equals":"x"}`,
		`{"field":"literal_keyword","not_equals":"x"}`,
		`{"field":"literal_keyword","not_in":["x"]}`,
		`{"field":"literal_keyword","gte":1}`,
		`{"field":"literal_keyword","equals_bool":true}`,
		// Two operators of the same kind on one leaf.
		`{"field":"protocol","in":["a"],"not_in":["b"]}`,
		`{"field":"protocol","equals":"a","not_equals":"b"}`,
		`{"field":"literal_keyword","contains_any":["a"],"not_contains_any":["b"]}`,
	} {
		if err := routingConditionError(t, when); err == nil {
			t.Fatalf("%s accepted", when)
		}
	}
}

func TestModelRoutingConditionRejectsMalformedBounds(t *testing.T) {
	for _, when := range []string{
		// A negative bound is refused in every spelling.
		`{"field":"turn_count","gt":-1}`,
		`{"field":"turn_count","gte":-1}`,
		`{"field":"turn_count","lt":-1}`,
		`{"field":"turn_count","lte":-1}`,
		`{"field":"turn_count","min":-1}`,
		`{"field":"turn_count","max":-1}`,
		// More than one bound per side, however it is spelled.
		`{"field":"turn_count","gt":1,"gte":2}`,
		`{"field":"turn_count","lt":1,"lte":2}`,
		`{"field":"turn_count","min":1,"gte":2}`,
		`{"field":"turn_count","min":1,"gt":2}`,
		`{"field":"turn_count","max":1,"lte":2}`,
		`{"field":"turn_count","max":1,"lt":2}`,
		// A lower bound above its upper bound, on one side or across families.
		`{"field":"turn_count","min":10,"max":5}`,
		`{"field":"turn_count","gte":10,"lte":5}`,
		`{"field":"turn_count","gt":10,"lt":5}`,
		`{"field":"turn_count","gte":6,"lt":5}`,
		`{"field":"turn_count","min":10,"lte":5}`,
		`{"field":"turn_count","gte":10,"max":5}`,
		// No bound at all.
		`{"field":"turn_count"}`,
		// A non-integer bound.
		`{"field":"turn_count","gte":"5"}`,
	} {
		if err := routingConditionError(t, when); err == nil {
			t.Fatalf("%s accepted", when)
		}
	}
}

func TestModelRoutingConditionBoundsKeywordAndStringLists(t *testing.T) {
	keywords := make([]string, MaxModelRoutingKeywords)
	for index := range keywords {
		keywords[index] = fmt.Sprintf("k%d", index)
	}
	encoded, err := json.Marshal(keywords)
	if err != nil {
		t.Fatal(err)
	}
	if err := routingConditionError(t, `{"field":"literal_keyword","not_contains_any":`+string(encoded)+`}`); err != nil {
		t.Fatalf("32 not_contains_any values rejected: %v", err)
	}
	overlong := append(append([]string{}, keywords...), "k32")
	encoded, err = json.Marshal(overlong)
	if err != nil {
		t.Fatal(err)
	}
	for _, when := range []string{
		`{"field":"literal_keyword","not_contains_any":` + string(encoded) + `}`,
		`{"field":"literal_keyword","not_contains_any":[]}`,
		`{"field":"literal_keyword","not_contains_any":[""]}`,
		`{"field":"protocol","not_in":[]}`,
	} {
		if err := routingConditionError(t, when); err == nil {
			t.Fatalf("%s accepted", when)
		}
	}
	// Value validation applies to the negated forms too.
	for _, when := range []string{
		`{"field":"task_type","not_in":["storyboard"]}`,
		`{"field":"task_type","not_equals":"storyboard"}`,
		`{"field":"media_kind","not_in":["hologram"]}`,
		`{"field":"media_kind","not_equals":"hologram"}`,
		`{"field":"language","not_in":["EN"]}`,
		`{"field":"language","not_equals":"en_US"}`,
	} {
		if err := routingConditionError(t, when); err == nil {
			t.Fatalf("%s accepted", when)
		}
	}
}

func TestModelRoutingConditionAcceptsClassifierFieldsOnGroup(t *testing.T) {
	// Absent: a group that predates the fields still validates.
	if err := routingConditionError(t, `{"field":"protocol","equals":"openai_chat"}`); err != nil {
		t.Fatalf("group without classifier fields rejected: %v", err)
	}
	// Well-formed: stored, and the values survive a decode round trip.
	document := `{"id":"g","enabled":true,"members":[{"id":"a","service_id":"service_01","model":"m"}],"default_member_id":"a","rules":[],
		"classifier_prompt":"classify the request","classifier_output_fmt":"keyword_match","classifier_json_path":"$.category",
		"classifier_regex":"^code","classifier_fallback":"general","classifier_timeout_ms":2500}`
	var group ModelRoutingGroup
	if err := json.Unmarshal([]byte(document), &group); err != nil {
		t.Fatal(err)
	}
	if err := group.Validate(); err != nil {
		t.Fatalf("well-formed classifier fields rejected: %v", err)
	}
	if group.ClassifierOutputFmt != "keyword_match" || group.ClassifierTimeoutMS == nil || *group.ClassifierTimeoutMS != 2500 {
		t.Fatalf("classifier fields did not survive decode: %+v", group)
	}
	reencoded, err := json.Marshal(group)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip ModelRoutingGroup
	if err := json.Unmarshal(reencoded, &roundTrip); err != nil {
		t.Fatalf("classifier fields did not round-trip: %v", err)
	}
	if roundTrip.ClassifierPrompt != "classify the request" || roundTrip.ClassifierFallback != "general" {
		t.Fatalf("classifier fields changed on round trip: %+v", roundTrip)
	}
	// Every documented output format is accepted, and the value set is closed.
	for _, format := range ModelRoutingClassifierOutputFormats {
		body := strings.Replace(document, `"keyword_match"`, `"`+format+`"`, 1)
		if err := json.Unmarshal([]byte(body), &group); err != nil {
			t.Fatal(err)
		}
		if err := group.Validate(); err != nil {
			t.Fatalf("classifier_output_fmt %q rejected: %v", format, err)
		}
	}
}

func TestModelRoutingGroupRejectsMalformedClassifierFields(t *testing.T) {
	for _, field := range []string{
		`"classifier_output_fmt":"yaml"`,
		`"classifier_output_fmt":"JSON"`,
		`"classifier_output_fmt":7`,
		`"classifier_output_fmt":null`,
		`"classifier_timeout_ms":0`,
		`"classifier_timeout_ms":60001`,
		`"classifier_timeout_ms":-1`,
		`"classifier_timeout_ms":"500"`,
		`"classifier_timeout_ms":null`,
		`"classifier_prompt":"` + strings.Repeat("p", MaxModelRoutingClassifierPromptRunes+1) + `"`,
		`"classifier_json_path":"` + strings.Repeat("j", MaxModelRoutingClassifierJSONPathRunes+1) + `"`,
		`"classifier_regex":"` + strings.Repeat("r", MaxModelRoutingClassifierRegexRunes+1) + `"`,
		`"classifier_fallback":"` + strings.Repeat("f", MaxModelRoutingClassifierFallbackRunes+1) + `"`,
	} {
		document := `{"id":"g","enabled":true,"members":[{"id":"a","service_id":"service_01","model":"m"}],"default_member_id":"a","rules":[],` + field + `}`
		var group ModelRoutingGroup
		if err := json.Unmarshal([]byte(document), &group); err != nil {
			continue
		}
		if err := group.Validate(); err == nil {
			t.Fatalf("%s accepted", field)
		}
	}
	// The per-field limits are inclusive: the boundary value is accepted.
	for _, field := range []string{
		`"classifier_prompt":"` + strings.Repeat("p", MaxModelRoutingClassifierPromptRunes) + `"`,
		`"classifier_json_path":"` + strings.Repeat("j", MaxModelRoutingClassifierJSONPathRunes) + `"`,
		`"classifier_regex":"` + strings.Repeat("r", MaxModelRoutingClassifierRegexRunes) + `"`,
		`"classifier_fallback":"` + strings.Repeat("f", MaxModelRoutingClassifierFallbackRunes) + `"`,
		// An empty string means "not configured" for every classifier_* field.
		`"classifier_output_fmt":""`,
		`"classifier_prompt":""`,
		`"classifier_timeout_ms":1`,
		`"classifier_timeout_ms":60000`,
	} {
		document := `{"id":"g","enabled":true,"members":[{"id":"a","service_id":"service_01","model":"m"}],"default_member_id":"a","rules":[],` + field + `}`
		var group ModelRoutingGroup
		if err := json.Unmarshal([]byte(document), &group); err != nil {
			t.Fatal(err)
		}
		if err := group.Validate(); err != nil {
			t.Fatalf("boundary %s rejected: %v", field, err)
		}
	}
}

func TestModelRoutingGroupTaskTypeLabelsAreScopedAndBackwardCompatible(t *testing.T) {
	legacy := validTestModelRoutingGroup()
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy group rejected: %v", err)
	}
	if !legacy.ValidTaskTypeLabel("coding") || legacy.ValidTaskTypeLabel("custom") {
		t.Fatalf("legacy effective labels = %+v", EffectiveModelRoutingTaskTypeLabels(legacy.TaskTypeLabels))
	}

	custom := validTestModelRoutingGroup()
	custom.TaskTypeLabels = []string{"custom", "review"}
	custom.Rules[0].When = ModelRoutingCondition{Field: "task_type", In: []string{"custom"}}
	if err := custom.Validate(); err != nil {
		t.Fatalf("custom group rejected: %v", err)
	}
	if !custom.ValidTaskTypeLabel("custom") || custom.ValidTaskTypeLabel("coding") {
		t.Fatalf("custom effective labels = %+v", EffectiveModelRoutingTaskTypeLabels(custom.TaskTypeLabels))
	}

	encoded, err := json.Marshal(custom)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip ModelRoutingGroup
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip.TaskTypeLabels, custom.TaskTypeLabels) {
		t.Fatalf("task_type_labels lost on round trip: got %v want %v", roundTrip.TaskTypeLabels, custom.TaskTypeLabels)
	}
}

func TestModelRoutingGroupRejectsTaskTypeOutsideDeclaredLabels(t *testing.T) {
	group := validTestModelRoutingGroup()
	group.TaskTypeLabels = []string{"custom"}
	group.Rules[0].When = ModelRoutingCondition{Field: "task_type", In: []string{"coding"}}
	err := group.Validate()
	if err == nil || !strings.Contains(err.Error(), `task_type "coding" is invalid`) || !strings.Contains(err.Error(), "custom") {
		t.Fatalf("error = %v, want invalid value and allowed collection", err)
	}
}

func TestModelRoutingGroupRejectsInvalidTaskTypeLabels(t *testing.T) {
	for _, labels := range [][]string{{""}, {"custom", "custom"}} {
		group := validTestModelRoutingGroup()
		group.TaskTypeLabels = labels
		if err := group.Validate(); err == nil {
			t.Fatalf("labels %v accepted", labels)
		}
	}
	tooMany := make([]string, MaxModelRoutingTaskTypeLabels+1)
	for index := range tooMany {
		tooMany[index] = fmt.Sprintf("label-%d", index)
	}
	group := validTestModelRoutingGroup()
	group.TaskTypeLabels = tooMany
	if err := group.Validate(); err == nil {
		t.Fatal("too many task_type_labels accepted")
	}
}

func TestModelRoutingConditionTimeLeaf(t *testing.T) {
	for _, when := range []string{
		`{"field":"system_time","within":["09:00","12:00"]}`,
		`{"field":"system_time","within":["00:00","23:59"]}`,
		// Equal endpoints name a single instant, not a minute or a day.
		`{"field":"system_time","within":["09:00","09:00"]}`,
		// A cross-midnight window: the start above the end.
		`{"field":"system_time","within":["22:00","06:00"]}`,
		// Exclusion composes through the not leaf, not a set operator.
		`{"not":{"field":"system_time","within":["09:00","12:00"]}}`,
		// The time leaf composes inside a tree.
		`{"all":[{"field":"system_time","within":["09:00","12:00"]},{"field":"task_type","in":["coding"]}]}`,
	} {
		if err := routingConditionError(t, when); err != nil {
			t.Fatalf("%s rejected: %v", when, err)
		}
	}
	for _, when := range []string{
		// Arity: the window is exactly two values.
		`{"field":"system_time","within":["09:00"]}`,
		`{"field":"system_time","within":["09:00","12:00","18:00"]}`,
		`{"field":"system_time","within":[]}`,
		// Format and range: HH:mm, 00-23 hours, 00-59 minutes.
		`{"field":"system_time","within":["9:00","12:00"]}`,
		`{"field":"system_time","within":["09:00","12:0"]}`,
		`{"field":"system_time","within":["09-00","12:00"]}`,
		`{"field":"system_time","within":["0900","12:00"]}`,
		`{"field":"system_time","within":["24:00","12:00"]}`,
		`{"field":"system_time","within":["09:60","12:00"]}`,
		// within belongs to system_time only.
		`{"field":"protocol","within":["09:00","12:00"]}`,
		`{"field":"turn_count","within":["09:00","12:00"]}`,
		`{"field":"literal_keyword","within":["09:00","12:00"]}`,
		// system_time takes no other operator, alone or combined.
		`{"field":"system_time"}`,
		`{"field":"system_time","equals":"09:00"}`,
		`{"field":"system_time","in":["09:00"]}`,
		`{"field":"system_time","within":["09:00","12:00"],"equals":"09:00"}`,
	} {
		if err := routingConditionError(t, when); err == nil {
			t.Fatalf("%s accepted", when)
		}
	}
}

func TestModelRoutingDecisionCandidateObservations(t *testing.T) {
	decision := validCandidateObservationDecision()
	decision.Fields[0].Candidates = []ModelRoutingCandidateObservation{
		{Value: "coding", Score: 0.81, Rank: 0, Chosen: true},
		{Value: "general", Score: 0.64, Rank: 1},
	}
	if err := decision.Validate(); err != nil {
		t.Fatalf("decision with candidates rejected: %v", err)
	}
	encoded, err := json.Marshal(decision)
	if err != nil {
		t.Fatal(err)
	}
	var roundTrip ModelRoutingDecision
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatalf("candidates do not round-trip: %v", err)
	}
	if !reflect.DeepEqual(roundTrip.Fields[0].Candidates, decision.Fields[0].Candidates) {
		t.Fatalf("candidates changed on round trip: %+v", roundTrip.Fields[0].Candidates)
	}

	// A field without candidates marshals without the key (omitempty), and a
	// record stored before candidates existed still parses and revalidates.
	legacy := validCandidateObservationDecision()
	legacyEncoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(legacyEncoded), "candidates") {
		t.Fatalf("empty candidates must stay off the wire: %s", legacyEncoded)
	}
	var legacyRoundTrip ModelRoutingDecision
	if err := json.Unmarshal(legacyEncoded, &legacyRoundTrip); err != nil {
		t.Fatalf("legacy decision rejected: %v", err)
	}
	if legacyRoundTrip.Fields[0].Candidates != nil {
		t.Fatalf("legacy decision gained candidates: %+v", legacyRoundTrip.Fields[0].Candidates)
	}

	for name, mutate := range map[string]func(*ModelRoutingFieldObservation){
		"two chosen": func(field *ModelRoutingFieldObservation) {
			field.Candidates = append(field.Candidates, ModelRoutingCandidateObservation{Value: "other", Chosen: true})
		},
		"score above one": func(field *ModelRoutingFieldObservation) {
			field.Candidates[0].Score = 1.5
		},
		"score is NaN": func(field *ModelRoutingFieldObservation) {
			field.Candidates[0].Score = math.NaN()
		},
		"negative rank": func(field *ModelRoutingFieldObservation) {
			field.Candidates[0].Rank = -1
		},
		"empty value": func(field *ModelRoutingFieldObservation) {
			field.Candidates[0].Value = ""
		},
	} {
		value := validCandidateObservationDecision()
		value.Fields[0].Candidates = []ModelRoutingCandidateObservation{{Value: "coding", Score: 0.8, Chosen: true}}
		mutate(&value.Fields[0])
		if err := value.Validate(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// A decision stored before candidates existed is a literal legacy document:
// no candidates key anywhere. It must still pass the strict decoder and
// Validate, and re-serialize without gaining the key — reading a stored
// record never upgrades it. Candidate-bearing records must serialize
// byte-stably so repeated reads of one stored row are indistinguishable.
func TestModelRoutingDecisionLegacyJSONAndByteStability(t *testing.T) {
	const legacy = `{"schema_version":1,"virtual_model_id":"astrlink/superclaude","group_id":"superclaude","config_snapshot_version":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","selection_source":"rule","rule_id":"coding","rule_priority":100,"used_default":false,"fields":[{"field":"task_type","source":"classifier","known":true,"match_count":1,"classifier_confidence":0.81,"classifier_id":"classifier_ab0123456789abcdef0123456789abcd"}],"member_id":"strong","member_service_id":"service_01","member_model":"claude-sonnet","execution":{"status":"succeeded","attempt_count":1},"privacy_attempts":[{"attempt_index":0,"service_id":"service_01","decision":"allow","finding_count":0,"duration_ms":2}],"duration_ms":23,"classifier_duration_ms":7}`
	var decoded ModelRoutingDecision
	if err := json.Unmarshal([]byte(legacy), &decoded); err != nil {
		t.Fatalf("legacy decision rejected: %v", err)
	}
	if err := decoded.Validate(); err != nil {
		t.Fatalf("legacy decision invalid: %v", err)
	}
	if decoded.Fields[0].Candidates != nil {
		t.Fatalf("legacy decision gained candidates: %+v", decoded.Fields[0].Candidates)
	}
	reencoded, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reencoded), "candidates") {
		t.Fatalf("reading a legacy record must not upgrade it: %s", reencoded)
	}
	again, err := json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reencoded, again) {
		t.Fatalf("repeated marshals differ:\n%s\n%s", reencoded, again)
	}

	modern := validCandidateObservationDecision()
	modern.Fields[0].Candidates = []ModelRoutingCandidateObservation{
		{Value: "coding", Score: 0.81, Rank: 0, Chosen: true},
		{Value: "general", Score: 0.64, Rank: 1, Chosen: false},
	}
	if err := modern.Validate(); err != nil {
		t.Fatal(err)
	}
	first, err := json.Marshal(modern)
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(modern)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("candidate-bearing marshals differ:\n%s\n%s", first, second)
	}
	if !strings.Contains(string(first), `"chosen":false`) {
		t.Fatalf("chosen:false must stay explicit on the wire: %s", first)
	}
	t.Logf("candidate-bearing wire sample:\n%s", first)
	var roundTrip ModelRoutingDecision
	if err := json.Unmarshal(first, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundTrip.Fields[0].Candidates, modern.Fields[0].Candidates) {
		t.Fatalf("candidates changed on round trip: %+v", roundTrip.Fields[0].Candidates)
	}
}

func validCandidateObservationDecision() ModelRoutingDecision {
	priority := 100
	return ModelRoutingDecision{
		SchemaVersion:         1,
		VirtualModelID:        "astrlink/superclaude",
		GroupID:               "superclaude",
		ConfigSnapshotVersion: strings.Repeat("a", 64),
		SelectionSource:       ModelRoutingSelectionRule,
		RuleID:                "coding",
		RulePriority:          &priority,
		Fields: []ModelRoutingFieldObservation{{
			Field:      "task_type",
			Source:     "classifier",
			Known:      true,
			MatchCount: 1,
		}},
		MemberID:        "strong",
		MemberServiceID: "service_01",
		MemberModel:     "claude-sonnet",
		Execution: ModelRoutingExecution{
			Status:       ModelRoutingExecutionSucceeded,
			AttemptCount: 1,
		},
		PrivacyAttempts: []ModelRoutingPrivacyAttempt{{
			AttemptIndex: 0,
			ServiceID:    "service_01",
			Decision:     ModelRoutingPrivacyAllow,
		}},
		DurationMS: 23,
	}
}

func validTestModelRoutingGroup() ModelRoutingGroup {
	return ModelRoutingGroup{
		ID:              "routing-group",
		Enabled:         true,
		Members:         []ModelRoutingMember{{ID: "fast", ServiceID: "service_01", Model: "fast"}, {ID: "strong", ServiceID: "service_01", Model: "strong"}},
		DefaultMemberID: "fast",
		Rules: []ModelRoutingRule{{
			ID:             "rule-1",
			Priority:       1,
			When:           ModelRoutingCondition{Field: "task_type", In: []string{"coding"}},
			TargetMemberID: "strong",
		}},
	}
}
