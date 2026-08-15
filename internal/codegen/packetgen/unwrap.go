package packetgen

import (
	"fmt"
	"strings"
)

// unwrapSwitchCompare adapts a reference that names a switch union so a second
// switch can discriminate on it.
//
// ProtoDef gives a switch field whatever its chosen branch produced, so a later
// switch compares against that value directly. Protocol 775 relies on it:
// scoreboard_objective's styling switches on number_format, which is itself a
// switch. A union struct carries no such value, only a field per case, so one
// is recovered here.
//
// The recovery is deliberately narrow. Every non-void case must carry the same
// optional type, which makes the present branch both unambiguous and
// observable. Anything else is an error naming what it found, because the
// alternative is guessing which branch a decoder should have believed.
func (b *builder) unwrapSwitchCompare(compare FieldReference, path string) (FieldReference, bool, error) {
	declaration, found := b.declaration(compare.GoType)
	if !found || declaration.Kind != DeclarationSwitch {
		// An optional field is the same situation without the union: the
		// switch compares against the value the option carries, and an absent
		// one matches nothing.
		return b.unwrapOptionalCompare(compare)
	}

	if len(declaration.Fields) == 0 {
		return compare, false, modelError(
			path,
			"switch compares against %s, which carries no value in any case",
			compare.GoType,
		)
	}

	caseType := declaration.Fields[0].GoType
	for _, field := range declaration.Fields[1:] {
		if field.GoType != caseType {
			return compare, false, modelError(
				path,
				"switch compares against %s, whose cases carry different types (%s and %s), so there is no single value to compare",
				compare.GoType,
				caseType,
				field.GoType,
			)
		}
	}
	if !strings.HasPrefix(caseType, "*") {
		return compare, false, modelError(
			path,
			"switch compares against %s, whose cases carry %s: without a pointer there is no way to tell which branch was decoded",
			compare.GoType,
			caseType,
		)
	}

	unwrapped := strings.TrimPrefix(caseType, "*")
	fields := make([]string, 0, len(declaration.Fields))
	for _, field := range declaration.Fields {
		fields = append(fields, field.GoName)
	}

	method := "compareValue"
	b.setUnwrap(compare.GoType, &SwitchUnwrap{Method: method, GoType: unwrapped, Fields: fields})

	compare.Value = fmt.Sprintf("%s.%s()", compare.Value, method)
	compare.GoType = unwrapped
	compare.Mapper = ""

	return compare, true, nil
}

// declaration finds a generated declaration by Go name, in the packet being
// compiled and then in the model's shared declarations.
func (b *builder) declaration(name string) (Declaration, bool) {
	if b.packet != nil {
		for _, declaration := range b.packet.Declarations {
			if declaration.Name == name {
				return declaration, true
			}
		}
	}
	for _, declaration := range b.model.Declarations {
		if declaration.Name == name {
			return declaration, true
		}
	}

	return Declaration{}, false
}

// setUnwrap records that a union needs its accessor emitted.
func (b *builder) setUnwrap(name string, unwrap *SwitchUnwrap) {
	if b.packet != nil {
		for index := range b.packet.Declarations {
			if b.packet.Declarations[index].Name == name {
				b.packet.Declarations[index].Unwrap = unwrap

				return
			}
		}
	}
	for index := range b.model.Declarations {
		if b.model.Declarations[index].Name == name {
			b.model.Declarations[index].Unwrap = unwrap

			return
		}
	}
}

// unwrapOptionalCompare adapts a reference to an optional scalar so a switch
// can discriminate on the value it carries.
//
// Only scalars are unwrapped. A switch comparing against an optional struct
// has no literal it could match, so such a reference is left alone and fails
// later where the case keys are checked, naming the type it actually found.
func (b *builder) unwrapOptionalCompare(compare FieldReference) (FieldReference, bool, error) {
	pointee, isPointer := strings.CutPrefix(compare.GoType, "*")
	if !isPointer || !comparableScalar(pointee) {
		return compare, false, nil
	}

	compare.Value = fmt.Sprintf("java.Deref(%s)", compare.Value)
	compare.GoType = pointee

	return compare, true, nil
}

// comparableScalar reports whether a Go type can appear as a switch case
// literal: the arithmetic scalars the wire uses, plus strings and booleans.
func comparableScalar(goType string) bool {
	if goType == "string" || goType == "bool" {
		return true
	}
	_, ok := ruleForGoType(goType)

	return ok
}
