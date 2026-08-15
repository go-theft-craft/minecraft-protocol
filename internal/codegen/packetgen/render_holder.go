package packetgen

import (
	"fmt"
	"strconv"
	"strings"
)

// renderDecodeTopBitSetArray reads elements until one arrives without its
// continuation bit.
//
// The bit belongs to the element's own first byte, so it is read and cleared
// before the element decodes. That keeps the element operations identical to
// any other array's: they never learn the array was framed this way.
func (r *operationRenderer) renderDecodeTopBitSetArray(output *sourceWriter, operation Operation) error {
	if !strings.HasPrefix(operation.GoType, "[]") {
		return fmt.Errorf("top-bit-terminated array has Go type %q", operation.GoType)
	}
	elementType := strings.TrimPrefix(operation.GoType, "[]")

	more := r.next("more")
	element := r.next("element")

	output.line("%s = make(%s, 0)", operation.Value, operation.GoType)
	output.line("for {")
	output.indent++
	output.line("%s, err := buffer.PeekTopBitSetContinues(%s)", more, strconv.Quote(operation.Path))
	r.renderReturnError(output)
	output.line(
		"if err := buffer.ValidateCollection(%s, len(%s)+1); err != nil {",
		strconv.Quote(operation.Path),
		operation.Value,
	)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	output.line("var %s %s", element, elementType)
	output.line("%s = append(%s, %s)", operation.Value, operation.Value, element)
	output.line("%s := len(%s) - 1", operation.Index, operation.Value)
	if err := r.renderDecodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.line("if !%s {", more)
	output.indent++
	output.line("break")
	output.indent--
	output.line("}")
	output.indent--
	output.line("}")

	return nil
}

// renderEncodeTopBitSetArray writes each element and then marks every one but
// the last as continuing.
//
// An empty array has no encoding at all: with no count and no terminator, it
// would put nothing on the wire and the next decode would read whatever came
// after as an element. It is refused rather than written.
func (r *operationRenderer) renderEncodeTopBitSetArray(output *sourceWriter, operation Operation) error {
	output.line("if len(%s) == 0 {", operation.Value)
	output.indent++
	output.line(
		"return fmt.Errorf(\"%%s: a top-bit-terminated array cannot be empty\", %s)",
		strconv.Quote(operation.Path),
	)
	output.indent--
	output.line("}")
	output.line("if err := buffer.ValidateCollection(%s, len(%s)); err != nil {", strconv.Quote(operation.Path), operation.Value)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")

	start := r.next("start")
	output.line("for %s := range %s {", operation.Index, operation.Value)
	output.indent++
	output.line("%s := buffer.Offset()", start)
	if err := r.renderEncodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.line("if %s != len(%s)-1 {", operation.Index, operation.Value)
	output.indent++
	output.line("if err := buffer.SetTopBitSetContinues(%s, %s); err != nil {", strconv.Quote(operation.Path), start)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	output.indent--
	output.line("}")
	output.indent--
	output.line("}")

	return nil
}

// renderDecodeHolder reads the biased discriminator and then, only when it says
// so, the inline value.
func (r *operationRenderer) renderDecodeHolder(output *sourceWriter, operation Operation) error {
	id := r.next("id")
	inline := r.next("inline")

	output.line("%s, %s, err := buffer.ReadHolder(%s)", id, inline, strconv.Quote(operation.Path))
	r.renderReturnError(output)
	output.line("if %s {", inline)
	output.indent++
	output.line("%s = new(%s)", selector(operation.Value, "Inline"), holderElementType(operation.GoType))
	if err := r.renderDecodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("} else {")
	output.indent++
	output.line("%s = %s", selector(operation.Value, "ID"), id)
	output.indent--
	output.line("}")

	return nil
}

func (r *operationRenderer) renderEncodeHolder(output *sourceWriter, operation Operation) error {
	inline := r.next("inline")

	output.line("%s := %s != nil", inline, selector(operation.Value, "Inline"))
	output.line(
		"if err := buffer.WriteHolder(%s, %s, %s); err != nil {",
		strconv.Quote(operation.Path),
		selector(operation.Value, "ID"),
		inline,
	)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	output.line("if %s {", inline)
	output.indent++
	if err := r.renderEncodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("}")

	return nil
}

// renderDecodeHolderSet reads the discriminator, then either a tag name or a
// bounded list of entries.
func (r *operationRenderer) renderDecodeHolderSet(output *sourceWriter, operation Operation) error {
	if len(operation.Operations) != 2 {
		return fmt.Errorf("holder set needs a tag and an entry operation, got %d", len(operation.Operations))
	}
	count := r.next("count")
	tagged := r.next("tagged")

	output.line("%s, %s, err := buffer.ReadHolderSet(%s)", count, tagged, strconv.Quote(operation.Path))
	r.renderReturnError(output)
	output.line("if %s {", tagged)
	output.indent++
	if err := r.renderDecodeOperation(output, operation.Operations[0]); err != nil {
		return err
	}
	output.indent--
	output.line("} else {")
	output.indent++
	output.line("%s = make(%s, %s)", selector(operation.Value, "IDs"), holderSetElementSlice(operation.GoType), count)
	output.line("for %s := range %s {", operation.Index, selector(operation.Value, "IDs"))
	output.indent++
	if err := r.renderDecodeOperation(output, operation.Operations[1]); err != nil {
		return err
	}
	output.indent--
	output.line("}")
	output.indent--
	output.line("}")

	return nil
}

func (r *operationRenderer) renderEncodeHolderSet(output *sourceWriter, operation Operation) error {
	if len(operation.Operations) != 2 {
		return fmt.Errorf("holder set needs a tag and an entry operation, got %d", len(operation.Operations))
	}
	tagged := r.next("tagged")

	// A nil ID slice is the tag branch. An empty non-nil slice is an explicit
	// empty set, which the biased count can express and a tag cannot.
	output.line("%s := %s == nil", tagged, selector(operation.Value, "IDs"))
	output.line(
		"if err := buffer.WriteHolderSet(%s, len(%s), %s); err != nil {",
		strconv.Quote(operation.Path),
		selector(operation.Value, "IDs"),
		tagged,
	)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	output.line("if %s {", tagged)
	output.indent++
	if err := r.renderEncodeOperation(output, operation.Operations[0]); err != nil {
		return err
	}
	output.indent--
	output.line("} else {")
	output.indent++
	output.line("for %s := range %s {", operation.Index, selector(operation.Value, "IDs"))
	output.indent++
	if err := r.renderEncodeOperation(output, operation.Operations[1]); err != nil {
		return err
	}
	output.indent--
	output.line("}")
	output.indent--
	output.line("}")

	return nil
}

// holderElementType extracts T from java.Holder[T].
func holderElementType(goType string) string {
	open := strings.Index(goType, "[")
	if open < 0 || !strings.HasSuffix(goType, "]") {
		return goType
	}

	return goType[open+1 : len(goType)-1]
}

// holderSetElementSlice returns []T for java.HolderSet[T].
func holderSetElementSlice(goType string) string {
	return "[]" + holderElementType(goType)
}
