package privacy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/QuantumNous/astrlink/core/contract"
)

var placeholderSkillDir = filepath.Join("..", "..", "..", "agent-bundle", placeholderSkillName)

var markdownCodeSpan = regexp.MustCompile("`([^`]+)`")

func readPlaceholderSkillFile(t *testing.T, name string) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(placeholderSkillDir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

// TestPlaceholderSkillShapesMatchTheAllocator keeps the skill's shape table in
// step with the placeholders the gateway emits. A model trusts the table, so a
// kind added to the allocator without a documented shape fails here.
func TestPlaceholderSkillShapesMatchTheAllocator(t *testing.T) {
	markers := map[string]Kind{}
	for _, kind := range contract.PrivacyKinds() {
		markers[strings.TrimSuffix(replacementFor(Kind(kind)), ">")+"_…>"] = Kind(kind)
	}
	documented := map[Kind]bool{}
	natural := map[Kind]bool{}
	for line := range strings.SplitSeq(readPlaceholderSkillFile(t, "references/shapes.md"), "\n") {
		cells := strings.Split(line, "|")
		if !strings.HasPrefix(line, "|") || len(cells) != 5 {
			continue
		}
		spans := markdownCodeSpan.FindAllStringSubmatch(cells[2], -1)
		if len(spans) != 1 {
			continue
		}
		marker := spans[0][1]
		kind, known := markers[marker]
		if !known || !isTokenPlaceholder(marker) {
			t.Fatalf("documented marker %q does not match an emitted shape", marker)
		}
		documented[kind] = true
		for _, example := range markdownCodeSpan.FindAllStringSubmatch(cells[3], -1) {
			if !isNaturalPlaceholder(kind, example[1]) {
				t.Fatalf("documented %s stand-in %q is not recognized", kind, example[1])
			}
			natural[kind] = true
		}
	}
	for _, kind := range contract.PrivacyKinds() {
		if !documented[Kind(kind)] {
			t.Fatalf("kind %s has no documented marker", kind)
		}
		_, hasNatural := naturalPlaceholderBuilders[Kind(kind)]
		if hasNatural != natural[Kind(kind)] {
			t.Fatalf("kind %s natural stand-in documented=%t, emitted=%t", kind, natural[Kind(kind)], hasNatural)
		}
	}
}

// TestPlaceholderSkillReachesTheUpstreamUnchanged runs the whole skill through
// the default policy, and with every kind enabled, wherever a host may carry
// it. A redacted skill would teach the model about placeholders that are not in
// the text it reads.
func TestPlaceholderSkillReachesTheUpstreamUnchanged(t *testing.T) {
	defaults := contract.DefaultPrivacyPolicy()
	defaults.Enabled = true
	everything := defaults
	everything.KindRules = contract.DefaultPrivacyKindRules()
	for index := range everything.KindRules {
		everything.KindRules[index].Enabled = true
	}
	recognized := map[string]int{}
	for _, source := range []contract.Policy{defaults, everything} {
		policy, err := FromContractPolicy(source)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"SKILL.md", "references/shapes.md"} {
			text := readPlaceholderSkillFile(t, name)
			encoded, err := json.Marshal(text)
			if err != nil {
				t.Fatal(err)
			}
			body := `{"system":` + string(encoded) + `,"messages":[` +
				`{"role":"user","content":` + string(encoded) + `},` +
				`{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"read","input":{"path":"SKILL.md"}}]},` +
				`{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":` + string(encoded) + `}]}]}`
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, contract.ProtocolAnthropicMessages, []byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != DecisionAllow || len(result.Findings) != 0 {
				t.Fatalf("%s: decision=%s findings=%#v", name, result.Decision, result.Findings)
			}
			for _, finding := range result.SuppressedFindings {
				switch finding.Suppression {
				case SuppressionPlaceholder:
					recognized[name]++
				case SuppressionKindDisabled:
				default:
					t.Fatalf("%s: suppressed for %s: %#v", name, finding.Suppression, finding)
				}
			}
		}
	}
	// The documented stand-ins are detected and then recognized, which also
	// proves the text was inspected at all.
	for _, name := range []string{"SKILL.md", "references/shapes.md"} {
		if recognized[name] == 0 {
			t.Fatalf("%s: no stand-in was detected", name)
		}
	}
}

func TestPlaceholderSkillStaysNeutral(t *testing.T) {
	skill := readPlaceholderSkillFile(t, "SKILL.md")
	for _, name := range []string{"SKILL.md", "references/shapes.md", "manifest.json"} {
		if strings.Contains(strings.ToLower(readPlaceholderSkillFile(t, name)), "astrlink") {
			t.Fatalf("%s names the gateway", name)
		}
	}
	frontmatter, _, found := strings.Cut(strings.TrimPrefix(skill, "---\n"), "\n---\n")
	if !found || !strings.Contains(frontmatter, "name: "+placeholderSkillName+"\n") {
		t.Fatalf("frontmatter does not name the skill: %q", frontmatter)
	}
	_, folded, found := strings.Cut(frontmatter, "description: >-\n")
	if !found {
		t.Fatal("description is not a folded block")
	}
	description := strings.Join(strings.Fields(folded), " ")
	if len(description) > 1024 {
		t.Fatalf("description is %d bytes, hosts accept at most 1024", len(description))
	}
}

func TestPlaceholderNoticeTellsTheTruthAboutToolArgumentRestore(t *testing.T) {
	const secret = `{"messages":[{"role":"user","content":"api_key=abcdefghijklmnop123456"}]}`
	for _, test := range []struct {
		name                string
		responseRestore     bool
		toolArgumentRestore bool
		want                string
	}{
		{"restored", true, true, placeholderNoticeRestored},
		{"tool arguments off", true, false, placeholderNoticeUnrestored},
		{"response restore off", false, true, placeholderNoticeUnrestored},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := tokenPolicy()
			policy.PlaceholderNotice = true
			policy.ResponseRestore = test.responseRestore
			policy.RestoreToolArguments = test.toolArgumentRestore
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, contract.ProtocolOpenAIChat, []byte(secret))
			if err != nil || !result.NoticeInjected {
				t.Fatalf("inspect: %#v %v", result, err)
			}
			var document struct {
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(result.Body, &document); err != nil {
				t.Fatal(err)
			}
			if len(document.Messages) != 2 || document.Messages[0].Role != "system" ||
				document.Messages[0].Content != test.want {
				t.Fatalf("notice message = %#v", document.Messages)
			}
		})
	}
	for _, notice := range []string{placeholderNoticeRestored, placeholderNoticeUnrestored} {
		lower := strings.ToLower(notice)
		if !noticeAlreadyPresent(notice) || strings.Contains(lower, "astrlink") ||
			strings.Contains(lower, "unavailable") {
			t.Fatalf("notice text drifted: %q", notice)
		}
	}
}

// A policy change between turns must not stack a second variant on top of the
// note already replayed in the history.
func TestPlaceholderNoticeVariantsShareTheDedupePhrase(t *testing.T) {
	policy := tokenPolicy()
	policy.PlaceholderNotice = true
	policy.ResponseRestore, policy.RestoreToolArguments = true, true
	first, err := mustTestEngine(t).Inspect(
		t.Context(), policy, contract.ProtocolOpenAIChat,
		[]byte(`{"messages":[{"role":"user","content":"mail alice@example.com"}]}`),
	)
	if err != nil || !first.NoticeInjected {
		t.Fatalf("first inspect: %#v %v", first, err)
	}
	policy.RestoreToolArguments = false
	second, err := mustTestEngine(t).Inspect(
		t.Context(), policy, contract.ProtocolOpenAIChat,
		appendChatMessage(t, first.Body, "and bob@example.com"),
	)
	if err != nil || second.Decision != DecisionRedact || second.NoticeInjected {
		t.Fatalf("second inspect: %#v %v", second, err)
	}
	if strings.Count(string(second.Body), "redaction markers of the form") != 1 {
		t.Fatalf("notice duplicated: %s", second.Body)
	}
}

const piSkillListing = "You are an expert coding assistant.\n\n" +
	"The following skills provide specialized instructions for specific tasks.\n" +
	"<available_skills>\n" +
	"  <skill>\n    <name>other-skill</name>\n    <description>Other.</description>\n" +
	"    <location>/home/user/.agents/skills/other-skill/SKILL.md</location>\n  </skill>\n" +
	"  <skill>\n    <name>redaction-placeholders</name>\n" +
	"    <description>Handle redaction placeholders such as &lt;SECRET_…&gt;.</description>\n" +
	"    <location>/home/user/.agents/skills/redaction-placeholders/SKILL.md</location>\n  </skill>\n" +
	"</available_skills>"

// codexSkillListing and claudeCodeSkillListing follow captured requests: Codex
// sends it as a developer input item, Claude Code as a system-role message.
const codexSkillListing = "<skills_instructions>\n## Skills\n" +
	"A skill is a set of local instructions to follow that is stored in a `SKILL.md` file.\n" +
	"### Skill roots\n- `r0` = `/home/user/.agents/skills`\n### Available skills\n" +
	"- other-skill: Other. (file: r0/other-skill/SKILL.md)\n" +
	"- redaction-placeholders: Handle redaction placeholders such as <SECRET_…>. (file: r0/redaction-placeholders/SKILL.md)\n" +
	"</skills_instructions>"

const claudeCodeSkillListing = "Send independent agent calls in a single message.\n\n" +
	"The following skills are available for use with the Skill tool:\n\n" +
	"- other-skill: Other.\nTRIGGER when the description wraps onto another line.\n" +
	"- redaction-placeholders: Handle redaction placeholders such as <SECRET_…>.\n" +
	"- last-skill: Last.\n\n" +
	"<total_tokens>1000 tokens left</total_tokens>"

// TestListedPlaceholderSkillReplacesTheNotice covers every protocol with a
// system channel. The skill explains the convention in more depth, so a second
// explanation would only cost tokens and perturb the cached prefix.
func TestListedPlaceholderSkillReplacesTheNotice(t *testing.T) {
	for host, text := range map[string]string{
		"pi":          piSkillListing,
		"codex":       codexSkillListing,
		"claude code": claudeCodeSkillListing,
	} {
		t.Run(host, func(t *testing.T) { testListedPlaceholderSkill(t, text) })
	}
}

func testListedPlaceholderSkill(t *testing.T, text string) {
	listing, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	const secret = `"api_key=abcdefghijklmnop123456"`
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
	}{
		{"responses instructions", contract.ProtocolOpenAIResponses, `{"instructions":__LISTING__,"input":[{"role":"user","content":` + secret + `}]}`},
		{"responses developer input", contract.ProtocolOpenAIResponses, `{"input":[{"role":"developer","content":[{"type":"input_text","text":__LISTING__}]},{"role":"user","content":` + secret + `}]}`},
		{"compact instructions", contract.ProtocolOpenAIResponsesCompact, `{"instructions":__LISTING__,"input":[{"role":"user","content":` + secret + `}]}`},
		{"anthropic string", contract.ProtocolAnthropicMessages, `{"system":__LISTING__,"messages":[{"role":"user","content":` + secret + `}]}`},
		{"anthropic blocks", contract.ProtocolAnthropicMessages, `{"system":[{"type":"text","text":"prefix"},{"type":"text","text":__LISTING__}],"messages":[{"role":"user","content":` + secret + `}]}`},
		{"anthropic system message", contract.ProtocolAnthropicMessages, `{"system":"prefix","messages":[{"role":"user","content":` + secret + `},{"role":"system","content":[{"type":"text","text":__LISTING__}]}]}`},
		{"chat system", contract.ProtocolOpenAIChat, `{"messages":[{"role":"system","content":__LISTING__},{"role":"user","content":` + secret + `}]}`},
		{"gemini", contract.ProtocolGoogleGenerateContent, `{"systemInstruction":{"parts":[{"text":__LISTING__}]},"contents":[{"role":"user","parts":[{"text":` + secret + `}]}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := tokenPolicy()
			policy.PlaceholderNotice = true
			body := strings.ReplaceAll(test.body, "__LISTING__", string(listing))
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, test.protocol, []byte(body))
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("inspect: %#v %v", result, err)
			}
			if !result.SkillListed || result.NoticeInjected ||
				strings.Contains(string(result.Body), "redaction markers of the form") {
				t.Fatalf("skill=%t notice=%t body=%s", result.SkillListed, result.NoticeInjected, result.Body)
			}
			if !strings.Contains(string(result.Body), string(listing)) {
				t.Fatalf("skill listing was modified: %s", result.Body)
			}
		})
	}
}

// Only a catalogue the host renders counts. The name in user or assistant text,
// or a system prompt that merely mentions it, does not load the skill.
func TestPlaceholderSkillMentionsOutsideTheHostListingKeepTheNotice(t *testing.T) {
	listing, err := json.Marshal(piSkillListing)
	if err != nil {
		t.Fatal(err)
	}
	other, err := json.Marshal(strings.ReplaceAll(piSkillListing, "<name>redaction-placeholders</name>", "<name>unrelated</name>"))
	if err != nil {
		t.Fatal(err)
	}
	codex, err := json.Marshal(codexSkillListing)
	if err != nil {
		t.Fatal(err)
	}
	codexOther, err := json.Marshal(strings.ReplaceAll(codexSkillListing, "- redaction-placeholders:", "- unrelated:"))
	if err != nil {
		t.Fatal(err)
	}
	claudeCode, err := json.Marshal(claudeCodeSkillListing)
	if err != nil {
		t.Fatal(err)
	}
	afterList, err := json.Marshal("The following skills are available for use with the Skill tool:\n\n" +
		"- other-skill: Other.\n\n- redaction-placeholders: Not part of the catalogue.")
	if err != nil {
		t.Fatal(err)
	}
	inDescription, err := json.Marshal("The following skills are available for use with the Skill tool:\n\n" +
		"- other-skill: Pairs with - redaction-placeholders: when loaded.")
	if err != nil {
		t.Fatal(err)
	}
	const secret = `"api_key=abcdefghijklmnop123456"`
	for _, test := range []struct {
		name     string
		protocol contract.ProtocolID
		body     string
	}{
		{"chat user", contract.ProtocolOpenAIChat, `{"messages":[{"role":"user","content":` + string(listing) + `},{"role":"user","content":` + secret + `}]}`},
		{"chat assistant", contract.ProtocolOpenAIChat, `{"messages":[{"role":"assistant","content":` + string(listing) + `},{"role":"user","content":` + secret + `}]}`},
		{"responses user", contract.ProtocolOpenAIResponses, `{"input":[{"role":"user","content":[{"type":"input_text","text":` + string(listing) + `}]},{"role":"user","content":` + secret + `}]}`},
		{"anthropic user", contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":` + string(listing) + `},{"role":"user","content":` + secret + `}]}`},
		{"gemini contents", contract.ProtocolGoogleGenerateContent, `{"contents":[{"role":"user","parts":[{"text":` + string(listing) + `}]},{"role":"user","parts":[{"text":` + secret + `}]}]}`},
		{"bare mention", contract.ProtocolOpenAIChat, `{"messages":[{"role":"system","content":"load redaction-placeholders"},{"role":"user","content":` + secret + `}]}`},
		{"other skill listed", contract.ProtocolAnthropicMessages, `{"system":` + string(other) + `,"messages":[{"role":"user","content":` + secret + `}]}`},
		{"codex listing in user input", contract.ProtocolOpenAIResponses, `{"input":[{"role":"user","content":[{"type":"input_text","text":` + string(codex) + `}]},{"role":"user","content":` + secret + `}]}`},
		{"codex other skill listed", contract.ProtocolOpenAIResponses, `{"input":[{"role":"developer","content":[{"type":"input_text","text":` + string(codexOther) + `}]},{"role":"user","content":` + secret + `}]}`},
		{"claude code listing in user message", contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"text","text":` + string(claudeCode) + `},{"type":"text","text":` + secret + `}]}]}`},
		{"claude code name after the list", contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":` + secret + `},{"role":"system","content":` + string(afterList) + `}]}`},
		{"claude code name inside a description", contract.ProtocolAnthropicMessages, `{"messages":[{"role":"user","content":` + secret + `},{"role":"system","content":` + string(inDescription) + `}]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy := tokenPolicy()
			policy.PlaceholderNotice = true
			result, err := mustTestEngine(t).Inspect(t.Context(), policy, test.protocol, []byte(test.body))
			if err != nil || result.Decision != DecisionRedact {
				t.Fatalf("inspect: %#v %v", result, err)
			}
			if result.SkillListed || !result.NoticeInjected {
				t.Fatalf("skill=%t notice=%t body=%s", result.SkillListed, result.NoticeInjected, result.Body)
			}
		})
	}
}
