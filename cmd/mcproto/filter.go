package main

import (
	"strconv"
	"strings"

	"github.com/go-theft-craft/minecraft-protocol/capture"
)

// A filter is a conjunction of terms, each "field<op>value", separated by
// spaces. There are no parentheses, no boolean operators, and no library.
//
// The grammar is small on purpose. A filter is typed at a shell prompt to
// narrow a few hundred thousand records to the interesting ones; anything it
// cannot express is a pipe to another tool away, and every operator it does
// have works on every field it names.
type filter struct {
	terms []filterTerm
}

type filterTerm struct {
	field string
	op    string
	value string
	// number is value parsed, for the fields that are numeric.
	number int64
}

// filterFields names every field a filter may mention, and whether it is
// numeric. An unknown field is a usage error naming it, because a filter that
// silently matched nothing would look like a connection with nothing in it.
var filterFields = map[string]bool{
	"kind":      false,
	"state":     false,
	"name":      false,
	"direction": false,
	"id":        true,
	"sequence":  true,
	"frame":     true,
	"bytes":     true,
}

// operators, longest first so "<=" is matched before "<".
var filterOperators = []string{"!=", ">=", "<=", "~=", "=", ">", "<"}

// parseFilter builds a filter from an expression. An empty expression matches
// everything.
func parseFilter(expression string) (filter, error) {
	var parsed filter

	for _, field := range strings.Fields(expression) {
		term, err := parseFilterTerm(field)
		if err != nil {
			return filter{}, err
		}
		parsed.terms = append(parsed.terms, term)
	}

	return parsed, nil
}

func parseFilterTerm(text string) (filterTerm, error) {
	for _, op := range filterOperators {
		index := strings.Index(text, op)
		if index <= 0 {
			continue
		}

		term := filterTerm{
			field: text[:index],
			op:    op,
			value: text[index+len(op):],
		}

		numeric, known := filterFields[term.field]
		if !known {
			return filterTerm{}, usagef(
				"unknown filter field %q; known fields: %s",
				term.field, strings.Join(sortedFilterFields(), ", "),
			)
		}
		if numeric {
			if term.op == "~=" {
				return filterTerm{}, usagef("field %q is a number and cannot be matched by substring", term.field)
			}
			number, err := strconv.ParseInt(strings.TrimPrefix(term.value, "0x"), base(term.value), 64)
			if err != nil {
				return filterTerm{}, usagef("field %q needs a number, got %q", term.field, term.value)
			}
			term.number = number
		} else if term.op == ">" || term.op == "<" || term.op == ">=" || term.op == "<=" {
			return filterTerm{}, usagef("field %q is text and cannot be ordered", term.field)
		}

		return term, nil
	}

	return filterTerm{}, usagef(
		"filter term %q needs an operator: one of %s",
		text, strings.Join(filterOperators, " "),
	)
}

func base(value string) int {
	if strings.HasPrefix(value, "0x") {
		return 16
	}

	return 10
}

func sortedFilterFields() []string {
	names := make([]string, 0, len(filterFields))
	for name := range filterFields {
		names = append(names, name)
	}
	// Sorted so the message is stable, which matters for a test and for a
	// person reading it twice.
	for outer := range names {
		for inner := outer + 1; inner < len(names); inner++ {
			if names[inner] < names[outer] {
				names[outer], names[inner] = names[inner], names[outer]
			}
		}
	}

	return names
}

// matches reports whether a record satisfies every term.
func (f filter) matches(record capture.Record) bool {
	for _, term := range f.terms {
		if !term.matches(record) {
			return false
		}
	}

	return true
}

func (t filterTerm) matches(record capture.Record) bool {
	if numeric := filterFields[t.field]; numeric {
		return compareNumbers(t.op, recordNumber(t.field, record), t.number)
	}

	return compareText(t.op, recordText(t.field, record), t.value)
}

func recordNumber(field string, record capture.Record) int64 {
	switch field {
	case "id":
		return int64(record.PacketID)
	case "sequence":
		return int64(record.Sequence)
	case "frame":
		return int64(record.Frame)
	case "bytes":
		if record.Redacted {
			return int64(record.OriginalLen)
		}

		return int64(len(record.Payload))
	default:
		return 0
	}
}

func recordText(field string, record capture.Record) string {
	switch field {
	case "kind":
		return kindName(record.Kind)
	case "state":
		return string(record.State)
	case "name":
		return record.Name
	case "direction":
		return directionName(record.Direction)
	default:
		return ""
	}
}

func compareNumbers(op string, got, want int64) bool {
	switch op {
	case "=":
		return got == want
	case "!=":
		return got != want
	case ">":
		return got > want
	case ">=":
		return got >= want
	case "<":
		return got < want
	case "<=":
		return got <= want
	default:
		return false
	}
}

func compareText(op, got, want string) bool {
	switch op {
	case "=":
		return got == want
	case "!=":
		return got != want
	case "~=":
		return strings.Contains(got, want)
	default:
		return false
	}
}
