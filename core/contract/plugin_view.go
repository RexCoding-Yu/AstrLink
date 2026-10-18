package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"unicode/utf8"
)

// Limits of what a plugin shows through the host. The host renders every
// view with its own components and every string as plain text.
const (
	MaxPluginViewBytes          = 16 << 10
	MaxPluginViewBlocks         = 32
	MaxPluginViewItems          = 50
	MaxPluginTableColumns       = 16
	MaxPluginTableRows          = 200
	MaxPluginViewTextRunes      = 1024
	MaxPluginRecordTitleRunes   = 32
	MaxPluginRecordSummaryRunes = 256
)

type PluginViewBlockType string

const (
	PluginViewKV     PluginViewBlockType = "kv"
	PluginViewTable  PluginViewBlockType = "table"
	PluginViewBadges PluginViewBlockType = "badges"
	PluginViewText   PluginViewBlockType = "text"
)

// PluginViewBlock is one block of a view a plugin returns: label-value rows
// (kv), a table, badges, or a paragraph of text.
type PluginViewBlock struct {
	Type PluginViewBlockType `json:"type"`
	// Items are the rows of a kv block or the badges of a badges block.
	Items []PluginViewItem `json:"items,omitempty"`
	// Columns and Rows make a table block. Each row has one cell a column.
	Columns []string           `json:"columns,omitempty"`
	Rows    [][]PluginViewCell `json:"rows,omitempty"`
	// Text is the text of a text block; it may span lines.
	Text string `json:"text,omitempty"`
}

// PluginViewItem is a kv row (Label, Value, Mono) or a badge (Text, Tone).
type PluginViewItem struct {
	Label string `json:"label,omitempty"`
	Value string `json:"value,omitempty"`
	// Mono shows Value in a monospace font, as for an identifier.
	Mono bool           `json:"mono,omitempty"`
	Text string         `json:"text,omitempty"`
	Tone PluginViewTone `json:"tone,omitempty"`
}

type PluginViewTone string

const (
	PluginViewToneOK    PluginViewTone = "ok"
	PluginViewToneWarn  PluginViewTone = "warn"
	PluginViewToneError PluginViewTone = "error"
)

// PluginViewCell is a table cell: a string, or a number when Number is set.
type PluginViewCell struct {
	Text   string
	Number *float64
}

func (cell PluginViewCell) MarshalJSON() ([]byte, error) {
	if cell.Number != nil {
		return json.Marshal(*cell.Number)
	}
	return json.Marshal(cell.Text)
}

func (cell *PluginViewCell) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) > 0 && data[0] == '"' {
		*cell = PluginViewCell{}
		return json.Unmarshal(data, &cell.Text)
	}
	number, err := strconv.ParseFloat(string(data), 64)
	if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
		return fmt.Errorf("a table cell must be a string or a number")
	}
	*cell = PluginViewCell{Number: &number}
	return nil
}

// ValidatePluginView checks a view against the limits the host renders it
// within.
func ValidatePluginView(view []PluginViewBlock) error {
	if len(view) > MaxPluginViewBlocks {
		return fmt.Errorf("a plugin view has at most %d blocks", MaxPluginViewBlocks)
	}
	for index, block := range view {
		if err := block.Validate(); err != nil {
			return fmt.Errorf("view block %d: %w", index, err)
		}
	}
	if encoded, err := json.Marshal(view); err != nil || len(encoded) > MaxPluginViewBytes {
		return fmt.Errorf("a plugin view must be at most %d KiB as JSON", MaxPluginViewBytes>>10)
	}
	return nil
}

func (block PluginViewBlock) Validate() error {
	switch block.Type {
	case PluginViewKV:
		if len(block.Items) == 0 || len(block.Items) > MaxPluginViewItems || block.Columns != nil || block.Rows != nil || block.Text != "" {
			return fmt.Errorf("a kv block has 1 to %d items and nothing else", MaxPluginViewItems)
		}
		for _, item := range block.Items {
			if item.Text != "" || item.Tone != "" {
				return fmt.Errorf("a kv item has a label and a value")
			}
			if err := validateBoundedText("label", item.Label, MaxPluginViewTextRunes, false); err != nil {
				return err
			}
			if err := validateBoundedText("value", item.Value, MaxPluginViewTextRunes, true); err != nil {
				return err
			}
		}
	case PluginViewBadges:
		if len(block.Items) == 0 || len(block.Items) > MaxPluginViewItems || block.Columns != nil || block.Rows != nil || block.Text != "" {
			return fmt.Errorf("a badges block has 1 to %d badges and nothing else", MaxPluginViewItems)
		}
		for _, item := range block.Items {
			if item.Label != "" || item.Value != "" || item.Mono {
				return fmt.Errorf("a badge has a text and a tone")
			}
			if err := validateBoundedText("badge", item.Text, MaxPluginViewTextRunes, false); err != nil {
				return err
			}
			if item.Tone != "" && item.Tone != PluginViewToneOK && item.Tone != PluginViewToneWarn && item.Tone != PluginViewToneError {
				return fmt.Errorf("unknown badge tone %q", item.Tone)
			}
		}
	case PluginViewTable:
		if len(block.Columns) == 0 || len(block.Columns) > MaxPluginTableColumns || len(block.Rows) > MaxPluginTableRows || block.Items != nil || block.Text != "" {
			return fmt.Errorf("a table has 1 to %d columns, at most %d rows and nothing else", MaxPluginTableColumns, MaxPluginTableRows)
		}
		for _, column := range block.Columns {
			if err := validateBoundedText("column", column, MaxPluginViewTextRunes, false); err != nil {
				return err
			}
		}
		for _, row := range block.Rows {
			if len(row) != len(block.Columns) {
				return fmt.Errorf("a table row has one cell a column")
			}
			for _, cell := range row {
				if cell.Number != nil && cell.Text != "" {
					return fmt.Errorf("a table cell is a string or a number")
				}
				if err := validateBoundedText("cell", cell.Text, MaxPluginViewTextRunes, true); err != nil {
					return err
				}
			}
		}
	case PluginViewText:
		if block.Items != nil || block.Columns != nil || block.Rows != nil {
			return fmt.Errorf("a text block has only text")
		}
		if err := validateMultilineText("text", block.Text, MaxPluginViewTextRunes); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown view block type %q", block.Type)
	}
	return nil
}

// validateMultilineText is validateBoundedText that also allows line breaks.
func validateMultilineText(field, value string, maxRunes int) error {
	if value == "" {
		return fmt.Errorf("%s must not be empty", field)
	}
	if !utf8.ValidString(value) || utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s must contain at most %d characters", field, maxRunes)
	}
	for _, runeValue := range value {
		if runeValue < 32 && runeValue != '\t' && runeValue != '\n' {
			return fmt.Errorf("%s must not contain control characters", field)
		}
	}
	return nil
}

type PluginRecordStatus string

const (
	PluginRecordOK      PluginRecordStatus = "ok"
	PluginRecordSkipped PluginRecordStatus = "skipped"
	PluginRecordFailed  PluginRecordStatus = "failed"
)

func (status PluginRecordStatus) Valid() bool {
	return status == PluginRecordOK || status == PluginRecordSkipped || status == PluginRecordFailed
}

// PluginRecord is what a plugin says about its part in a request or about
// itself: a title such as 「记忆」, a status, a one-line summary and a view.
type PluginRecord struct {
	Title   string             `json:"title"`
	Status  PluginRecordStatus `json:"status"`
	Summary string             `json:"summary"`
	View    []PluginViewBlock  `json:"view,omitempty"`
	// Truncated marks a record the host cut to its limits, dropping what
	// did not fit or was not valid.
	Truncated bool `json:"truncated,omitempty"`
}

func (record PluginRecord) Validate() error {
	if err := validateBoundedText("title", record.Title, MaxPluginRecordTitleRunes, false); err != nil {
		return err
	}
	if !record.Status.Valid() {
		return fmt.Errorf("unknown plugin record status %q", record.Status)
	}
	if err := validateBoundedText("summary", record.Summary, MaxPluginRecordSummaryRunes, true); err != nil {
		return err
	}
	return ValidatePluginView(record.View)
}
