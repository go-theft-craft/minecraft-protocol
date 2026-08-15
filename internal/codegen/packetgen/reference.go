package packetgen

import (
	"fmt"
	"strings"
)

// maxReferenceSegments is how deep a field reference may point. One segment
// names a field; two name one part of a field that unpacks into parts. Nothing
// in either supported protocol needs a third, and accepting one would mean
// guessing at what it addresses.
const maxReferenceSegments = 2

// resolvePath resolves a ProtoDef field reference against the scope chain.
//
// Three forms exist, and they scope differently:
//
//   - "$name" substitutes an alias argument and resolves it in the scope that
//     supplied it.
//   - "../name" names an exact level: as many hops out as there are prefixes,
//     and the field must be bound there.
//   - "name" resolves lexically, innermost scope first and then outward.
//
// Either of the latter two may carry one more segment naming a member of a
// bitfield or bitflags field, which is how protocol 775 discriminates on a
// single bit — "flags/has_redirect_node".
func resolvePath(current *scope, source string) (FieldReference, error) {
	head, member, err := splitReference(source)
	if err != nil {
		return FieldReference{}, err
	}

	reference, err := resolveReference(current, head)
	if err != nil {
		return FieldReference{}, err
	}
	if member == "" {
		reference.Source = source

		return reference, nil
	}

	part, ok := reference.Members[member]
	if !ok {
		if len(reference.Members) == 0 {
			return FieldReference{}, fmt.Errorf("field %q has no members to address, so %q cannot be reached", head, member)
		}

		return FieldReference{}, fmt.Errorf("field %q has no member %q", head, member)
	}
	part.Source = source
	// A member is a leaf. Addressing through it again is unsupported rather
	// than silently ignored.
	part.Members = nil

	return part, nil
}

// splitReference separates a reference into the field it names and, when
// present, the single member it addresses within that field. The "$" and "../"
// prefixes stay on the head, because they select the scope rather than the
// field.
func splitReference(source string) (head, member string, err error) {
	if source == "" {
		return "", "", fmt.Errorf("reference is empty")
	}

	var prefix, rest string
	if index := strings.LastIndex(source, "../"); index >= 0 {
		prefix = source[:index+len("../")]
		rest = source[index+len("../"):]
	} else {
		rest = source
	}

	segments := strings.Split(rest, "/")
	if len(segments) > maxReferenceSegments {
		return "", "", fmt.Errorf("reference %q addresses %d levels, and only %d are supported", source, len(segments), maxReferenceSegments)
	}
	for _, segment := range segments {
		if segment == "" {
			return "", "", fmt.Errorf("reference %q has an empty segment", source)
		}
	}

	if len(segments) == 1 {
		return prefix + segments[0], "", nil
	}

	return prefix + segments[0], segments[1], nil
}
