package privacy

import (
	"strings"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/systemprompt"
	"github.com/tidwall/gjson"
)

// The placeholder notices explain the token placeholder convention to the model.
//
// They are only needed for token-shaped placeholders. A natural stand-in is
// already a well-formed value of its type, so a model copies it verbatim
// without being told; an opaque marker is out-of-distribution text that models
// otherwise paraphrase, reformat, or refuse.
//
// Each variant states what actually happens to a marker written into a tool
// call, because a model told to report hidden values as unavailable refuses
// tasks the restore path would have completed. The restored variant also keeps
// secret markers out of replies: response restore would print the secret into
// the user's chat, where repeating it serves nothing. The notes deliberately
// carry no mapping: sending the originals alongside the placeholders would
// defeat the redaction entirely.
const placeholderNoticePrefix = "Some values in this conversation were replaced by " +
	"redaction markers of the form <PRIVATE_KIND_hex> or <SECRET_hex>. Treat each " +
	"marker as an opaque literal: copy it character for character when you need to " +
	"refer to it, never alter or invent one, and never guess the value behind it. "

const placeholderNoticeRestored = placeholderNoticePrefix +
	"Markers in tool-call arguments are replaced with the original values before the " +
	"tool runs, so write the marker exactly where the value belongs and perform any " +
	"encoding, hashing, or other transformation inside the tool. Replies are restored " +
	"the same way, so a <SECRET_hex> marker in a reply prints the secret: keep secret " +
	"markers out of replies unless the user asks for the literal value, and name the " +
	"secret instead."

const placeholderNoticeUnrestored = placeholderNoticePrefix +
	"Markers in tool-call arguments are not replaced with the original values, so " +
	"do not write a marker into a file, command, or request that needs the real " +
	"value; say that the value is hidden by local privacy settings instead."

// placeholderNoticeText selects the variant matching the effective tool
// argument restore setting.
func placeholderNoticeText(restoreToolArguments bool) string {
	if restoreToolArguments {
		return placeholderNoticeRestored
	}
	return placeholderNoticeUnrestored
}

// injectPlaceholderNotice prepends the convention note to the protocol's system
// instruction channel, after any official client identity that opens it. It
// reports whether the document was modified.
func injectPlaceholderNotice(document *jsonDocument, protocol contract.ProtocolID, notice string) (bool, error) {
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		// A client may replay the notice in input after converting protocols.
		if messagesContainNotice(gjson.GetBytes(document.body, "input")) ||
			contentContainsNotice(gjson.GetBytes(document.body, "instructions")) {
			return false, nil
		}
	case contract.ProtocolAnthropicMessages:
		if contentContainsNotice(gjson.GetBytes(document.body, "system")) {
			return false, nil
		}
	case contract.ProtocolOpenAIChat:
		if messagesContainNotice(gjson.GetBytes(document.body, "messages")) {
			return false, nil
		}
	case contract.ProtocolGoogleGenerateContent:
		if contentContainsNotice(gjson.GetBytes(document.body, "systemInstruction.parts")) ||
			contentContainsNotice(gjson.GetBytes(document.body, "system_instruction.parts")) {
			return false, nil
		}
	default:
		// A raw completion has no system channel, and prefixing the prompt would
		// change what the model is asked to continue.
		return false, nil
	}
	updated, changed, err := systemprompt.Prepend(protocol, document.body, notice)
	if err != nil {
		return false, err
	}
	document.body = updated
	return changed, nil
}

// placeholderSkillName is the agent skill that teaches the same convention in
// more depth. A host that already lists it does not need the notice.
const placeholderSkillName = "redaction-placeholders"

// placeholderSkillListed reports whether the host-generated system channel
// already offers the placeholder skill. It only reads the channels a client
// writes itself; user and assistant content may quote the skill name without
// the skill being loaded, and nothing in the request is modified.
func placeholderSkillListed(protocol contract.ProtocolID, body []byte) bool {
	switch protocol {
	case contract.ProtocolOpenAIResponses, contract.ProtocolOpenAIResponsesCompact:
		instructions := gjson.GetBytes(body, "instructions")
		return instructions.Type == gjson.String && skillListingNames(instructions.Str) ||
			systemMessagesListSkill(gjson.GetBytes(body, "input"))
	case contract.ProtocolAnthropicMessages:
		// Claude Code sends its catalogue as a system-role message after the
		// first user turn rather than in the top-level system prompt.
		return contentListsSkill(gjson.GetBytes(body, "system")) ||
			systemMessagesListSkill(gjson.GetBytes(body, "messages"))
	case contract.ProtocolOpenAIChat:
		return systemMessagesListSkill(gjson.GetBytes(body, "messages"))
	case contract.ProtocolGoogleGenerateContent:
		return contentListsSkill(gjson.GetBytes(body, "systemInstruction.parts"))
	default:
		return false
	}
}

func systemMessagesListSkill(messages gjson.Result) bool {
	if messages.IsArray() {
		for _, message := range messages.Array() {
			role := message.Get("role").Str
			if (role == "system" || role == "developer") && contentListsSkill(message.Get("content")) {
				return true
			}
		}
	}
	return false
}

func contentListsSkill(content gjson.Result) bool {
	if content.Type == gjson.String {
		return skillListingNames(content.Str)
	}
	if content.IsArray() {
		for _, block := range content.Array() {
			if text := block.Get("text"); text.Type == gjson.String && skillListingNames(text.Str) {
				return true
			}
		}
	}
	return false
}

// skillListingNames matches the skill catalogue a host renders into its system
// prompt, not a bare mention of the name:
//   - Pi renders an <available_skills> block with one <name> element per skill.
//   - Codex renders a <skills_instructions> block with one "- name: …" line per
//     skill.
//   - Claude Code renders "- name: …" lines after a fixed heading, ending at a
//     blank line.
func skillListingNames(text string) bool {
	return anyTaggedBlock(text, "<available_skills>", "</available_skills>", func(block string) bool {
		return strings.Contains(block, "<name>"+placeholderSkillName+"</name>")
	}) || anyTaggedBlock(text, "<skills_instructions>", "</skills_instructions>", bulletListsSkill) ||
		anyTaggedBlock(text, "The following skills are available for use with the Skill tool:\n\n", "\n\n", bulletListsSkill)
}

// anyTaggedBlock reports whether match accepts any block between open and the
// next close. A block without its close runs to the end of the text.
func anyTaggedBlock(text, open, close string, match func(string) bool) bool {
	for {
		start := strings.Index(text, open)
		if start < 0 {
			return false
		}
		text = text[start+len(open):]
		end := strings.Index(text, close)
		if end < 0 {
			return match(text)
		}
		if match(text[:end]) {
			return true
		}
		text = text[end+len(close):]
	}
}

func bulletListsSkill(block string) bool {
	for line := range strings.Lines(block) {
		if strings.HasPrefix(line, "- "+placeholderSkillName+":") {
			return true
		}
	}
	return false
}

func noticeAlreadyPresent(value string) bool {
	return strings.Contains(value, "redaction markers of the form")
}

// Scan every system/developer message before choosing where to insert. Clients
// may add a new prefix ahead of an already annotated message on the next turn.
// User or assistant quotations do not count as system-channel instructions.
func messagesContainNotice(messages gjson.Result) bool {
	if messages.IsArray() {
		for _, message := range messages.Array() {
			role := message.Get("role").Str
			if (role == "system" || role == "developer") && contentContainsNotice(message.Get("content")) {
				return true
			}
		}
	}
	return false
}

func contentContainsNotice(content gjson.Result) bool {
	if content.Type == gjson.String {
		return noticeAlreadyPresent(content.Str)
	}
	if content.IsArray() {
		for _, block := range content.Array() {
			if text := block.Get("text"); text.Type == gjson.String && noticeAlreadyPresent(text.Str) {
				return true
			}
		}
	}
	return false
}
