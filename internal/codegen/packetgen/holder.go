package packetgen

import (
	"fmt"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
)

// compileTopBitSetArray compiles an array that carries no count, ending instead
// when an element arrives without the top bit of its first byte set.
//
// The bit is stolen from the element's own first byte, so the element type must
// begin with something a byte wide. Nothing enforces that here — the schema's
// one use starts with an i8 slot — but the encoding silently corrupts an
// element whose first field needs its high bit, so a wider first field is
// rejected where it can be seen.
func (b *builder) compileTopBitSetArray(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
) (compiledValue, error) {
	var elementNode *protodef.TypeNode
	for _, argument := range node.Arguments {
		if argument.Name != "type" {
			return compiledValue{}, modelError(
				path,
				"%w: native %q does not accept argument %q",
				ErrUnknownNativeArgument,
				node.Name,
				argument.Name,
			)
		}
		elementNode = argument.Type
	}
	if elementNode == nil {
		return compiledValue{}, modelError(path, "%w: native %q needs an element type", ErrUnknownNativeArgument, node.Name)
	}

	index := fmt.Sprintf("index%d", b.arrayIndex)
	b.arrayIndex++

	element, err := b.compileNode(elementNode, desiredName+"Item", path+"[]", indexed(value, index), current, "")
	if err != nil {
		return compiledValue{}, err
	}
	goType := "[]" + element.goType

	return compiledValue{
		goType: goType,
		decode: Operation{
			Kind: OpTopBitSetArray, Value: value, GoType: goType, Path: path,
			Index: index, Operations: []Operation{element.decode},
		},
		encode: Operation{
			Kind: OpTopBitSetArray, Value: value, GoType: goType, Path: path,
			Index: index, Operations: []Operation{element.encode},
		},
	}, nil
}

// compileHolder compiles a value that is either a registry reference or an
// inline value, discriminated by a biased VarInt.
//
// The schema names both halves — baseName for the ID, otherwise.name for the
// inline value — but the generated Go type is java.Holder, whose fields are
// fixed. The source names are kept in the model so the coverage report and any
// differential comparison can map between the two.
func (b *builder) compileHolder(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
) (compiledValue, error) {
	var (
		baseName   string
		inlineName string
		inlineNode *protodef.TypeNode
	)
	for _, argument := range node.Arguments {
		switch argument.Name {
		case "baseName":
			baseName = argument.String
		case "otherwise":
			inlineName, inlineNode = argument.FieldName, argument.Type
		default:
			return compiledValue{}, modelError(
				path,
				"%w: native %q does not accept argument %q",
				ErrUnknownNativeArgument,
				node.Name,
				argument.Name,
			)
		}
	}
	if baseName == "" {
		return compiledValue{}, modelError(path, "%w: native %q needs baseName", ErrUnknownNativeArgument, node.Name)
	}
	if inlineNode == nil {
		return compiledValue{}, modelError(path, "%w: native %q needs an otherwise type", ErrUnknownNativeArgument, node.Name)
	}

	inline, err := b.compileNode(inlineNode, desiredName+"Inline", path+".inline", dereference(selector(value, "Inline")), current, "")
	if err != nil {
		return compiledValue{}, err
	}
	goType := "java.Holder[" + inline.goType + "]"

	return compiledValue{
		goType: goType,
		decode: Operation{
			Kind: OpHolder, Value: value, GoType: goType, Path: path,
			SourceNames: []string{baseName, inlineName}, Operations: []Operation{inline.decode},
		},
		encode: Operation{
			Kind: OpHolder, Value: value, GoType: goType, Path: path,
			SourceNames: []string{baseName, inlineName}, Operations: []Operation{inline.encode},
		},
	}, nil
}

// compileHolderSet compiles a value that is either a registry tag name or an
// explicit list of entries.
//
// The tag half is a string in every use the schema has, and java.HolderSet
// types it as one. A schema that gave the tag another type would need a second
// parameter on the Go type, so it is refused rather than compiled into
// something that would not round-trip.
func (b *builder) compileHolderSet(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
) (compiledValue, error) {
	var (
		tagName    string
		tagNode    *protodef.TypeNode
		entryName  string
		entryNode  *protodef.TypeNode
		haveBase   bool
		haveEntrie bool
	)
	for _, argument := range node.Arguments {
		switch argument.Name {
		case "base":
			tagName, tagNode, haveBase = argument.FieldName, argument.Type, true
		case "otherwise":
			entryName, entryNode, haveEntrie = argument.FieldName, argument.Type, true
		default:
			return compiledValue{}, modelError(
				path,
				"%w: native %q does not accept argument %q",
				ErrUnknownNativeArgument,
				node.Name,
				argument.Name,
			)
		}
	}
	if !haveBase || tagNode == nil {
		return compiledValue{}, modelError(path, "%w: native %q needs a base type", ErrUnknownNativeArgument, node.Name)
	}
	if !haveEntrie || entryNode == nil {
		return compiledValue{}, modelError(path, "%w: native %q needs an otherwise type", ErrUnknownNativeArgument, node.Name)
	}

	tag, err := b.compileNode(tagNode, desiredName+"Tag", path+".tag", selector(value, "Tag"), current, "")
	if err != nil {
		return compiledValue{}, err
	}
	if tag.goType != "string" {
		return compiledValue{}, modelError(path, "%w: native %q tags must be strings, not %s", ErrUnknownNativeArgument, node.Name, tag.goType)
	}

	index := fmt.Sprintf("index%d", b.arrayIndex)
	b.arrayIndex++

	entry, err := b.compileNode(entryNode, desiredName+"Entry", path+".ids[]", indexed(selector(value, "IDs"), index), current, "")
	if err != nil {
		return compiledValue{}, err
	}
	goType := "java.HolderSet[" + entry.goType + "]"

	return compiledValue{
		goType: goType,
		decode: Operation{
			Kind: OpHolderSet, Value: value, GoType: goType, Path: path, Index: index,
			SourceNames: []string{tagName, entryName},
			Operations:  []Operation{tag.decode, entry.decode},
		},
		encode: Operation{
			Kind: OpHolderSet, Value: value, GoType: goType, Path: path, Index: index,
			SourceNames: []string{tagName, entryName},
			Operations:  []Operation{tag.encode, entry.encode},
		},
	}, nil
}
