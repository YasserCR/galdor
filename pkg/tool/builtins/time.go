package builtins

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/YasserCR/galdor/pkg/tool"
)

// TimeIn is the input shape of the time tool. Operation selects which
// time operation to run; the other fields are interpreted per
// operation. See NewTimeTool's docstring for the operation table.
type TimeIn struct {
	Operation string `json:"operation" jsonschema:"What to do: now | parse | format,enum=now;parse;format"`

	// Value is the input value for parse (an RFC 3339 / custom-format
	// date-time string) or format (an integer Unix timestamp in
	// seconds, encoded as a string for cross-provider safety).
	Value string `json:"value,omitempty" jsonschema:"Operation input — see operation table"`

	// Layout is the Go time-package layout string for parse and format
	// (default time.RFC3339). Use Go's reference time
	// 'Mon Jan 2 15:04:05 MST 2006' to construct custom layouts.
	Layout string `json:"layout,omitempty" jsonschema:"Go time layout; defaults to RFC3339"`

	// TZ is an IANA time zone (e.g. 'America/Guayaquil'). Default UTC.
	TZ string `json:"tz,omitempty" jsonschema:"IANA timezone; defaults to UTC"`
}

// TimeOut carries the result of any time operation.
type TimeOut struct {
	// Formatted is the formatted string output for `now` and `format`.
	Formatted string `json:"formatted,omitempty"`

	// Unix is the seconds-since-epoch representation. Populated by
	// `now` and `parse`.
	Unix int64 `json:"unix,omitempty"`

	// TZ is the IANA timezone the result was rendered in.
	TZ string `json:"tz,omitempty"`
}

// NewTimeTool returns a tool that exposes three operations:
//
//	now    → current time. Output: Formatted (in Layout/TZ) + Unix.
//	parse  → Value parsed against Layout. Output: Unix + Formatted (RFC3339, TZ-aware).
//	format → Unix value (encoded in Value) formatted under Layout/TZ. Output: Formatted.
//
// The tool has no side effects. It is safe to enable for any agent.
func NewTimeTool() (tool.Tool[TimeIn, TimeOut], error) {
	return tool.NewTool("time",
		"Look up or transform timestamps. Use operation=now/parse/format.",
		runTime)
}

// MustNewTimeTool is the panicking variant of NewTimeTool.
func MustNewTimeTool() tool.Tool[TimeIn, TimeOut] {
	return tool.MustNewTool("time",
		"Look up or transform timestamps. Use operation=now/parse/format.", runTime)
}

func runTime(_ context.Context, in TimeIn) (TimeOut, error) {
	loc, err := loadLocation(in.TZ)
	if err != nil {
		return TimeOut{}, err
	}
	layout := in.Layout
	if layout == "" {
		layout = time.RFC3339
	}

	switch strings.ToLower(strings.TrimSpace(in.Operation)) {
	case "", "now":
		now := time.Now().In(loc)
		return TimeOut{
			Formatted: now.Format(layout),
			Unix:      now.Unix(),
			TZ:        loc.String(),
		}, nil

	case "parse":
		if in.Value == "" {
			return TimeOut{}, fmt.Errorf("time: parse needs value")
		}
		t, err := time.ParseInLocation(layout, in.Value, loc)
		if err != nil {
			return TimeOut{}, fmt.Errorf("time: parse: %w", err)
		}
		t = t.In(loc)
		return TimeOut{
			Formatted: t.Format(time.RFC3339),
			Unix:      t.Unix(),
			TZ:        loc.String(),
		}, nil

	case "format":
		if in.Value == "" {
			return TimeOut{}, fmt.Errorf("time: format needs value (unix seconds)")
		}
		var unix int64
		if _, err := fmt.Sscanf(in.Value, "%d", &unix); err != nil {
			return TimeOut{}, fmt.Errorf("time: format value must be integer unix seconds, got %q", in.Value)
		}
		t := time.Unix(unix, 0).In(loc)
		return TimeOut{
			Formatted: t.Format(layout),
			Unix:      unix,
			TZ:        loc.String(),
		}, nil

	default:
		return TimeOut{}, fmt.Errorf("time: unknown operation %q (want now/parse/format)", in.Operation)
	}
}

func loadLocation(tz string) (*time.Location, error) {
	if tz == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, fmt.Errorf("time: unknown timezone %q: %w", tz, err)
	}
	return loc, nil
}
