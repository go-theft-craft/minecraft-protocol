package packetgen

import (
	"errors"
	"fmt"
	"sort"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
)

// ErrDeadNativeCodec reports a hand-written codec whose name the schema does
// not declare native.
//
// It is an error rather than a warning because a dead entry is indistinguishable
// from a live one until a schema happens to define that name, at which point
// the override silently wins and produces wrong bytes. That is exactly the
// failure this file exists to make impossible.
var ErrDeadNativeCodec = errors.New("hand-written codec is not native in this schema")

// ErrUnknownNativeArgument reports a native invoked with an argument its
// hand-written codec does not accept.
var ErrUnknownNativeArgument = errors.New("unsupported native argument")

// nativeCodecs are the hand-written codecs, keyed by the schema name each one
// backs.
//
// A name in this table is used only when the schema being compiled declares
// that name native. Every other type is compiled from its own definition, even
// when a codec of the same name exists. This is the rule the whole file is
// about: protocol 47 and protocol 775 both define `position` and
// `entityMetadata`, with different bit orders and different terminators, so a
// codec bound by bare name gives one of the two protocols the other's wire
// format. A round-trip test cannot catch that, because both directions are
// wrong together.
//
// The arithmetic scalars live in basicScalarRules and are exempt from the dead
// check: they are ProtoDef's own built-ins, and a schema may reference one
// without listing it.
var nativeCodecs = map[string]scalarRule{
	"UUID":       {goType: "java.UUID", read: "ReadUUID", write: "WriteUUID"},
	"pstring":    {goType: "string", read: "ReadString", write: "WriteString"},
	"restBuffer": {goType: "[]byte", read: "ReadRestBuffer", write: "WriteRestBuffer"},
	"void":       {},

	// Protocol 47's NBT carries a name on its root compound. Protocol 775's
	// does not. They are separate Go types for the same reason they are
	// separate entries here: the encodings differ by exactly that name, so a
	// value of one written where the other is expected parses as something
	// else instead of failing.
	"nbt":             {goType: "java.NBT", read: "ReadNBT", write: "WriteNBT"},
	"optionalNbt":     {goType: "*java.NBT", read: "ReadOptionalNBT", write: "WriteOptionalNBT"},
	"anonymousNbt":    {goType: "java.NetworkNBT", read: "ReadAnonymousNBT", write: "WriteAnonymousNBT"},
	"anonOptionalNbt": {goType: "*java.NetworkNBT", read: "ReadAnonOptionalNBT", write: "WriteAnonOptionalNBT"},

	"lpVec3": {goType: "java.LPVec3", read: "ReadLPVec3", write: "WriteLPVec3"},
}

// structuralNatives are natives the compiler handles by kind rather than with a
// codec: the schema parser turns them into container, array, switch, option,
// buffer, bitfield, and bitflags nodes.
var structuralNatives = map[string]struct{}{
	"container":          {},
	"array":              {},
	"switch":             {},
	"option":             {},
	"buffer":             {},
	"bitfield":           {},
	"bitflags":           {},
	"mapper":             {},
	"entityMetadataLoop": {},

	// Protocol 775's parameterized natives are structural for the same reason
	// an array is: each one wraps a type the schema supplies, so there is
	// nothing for a single hand-written codec to bind to. The framing they add
	// lives in wire/java; the element inside them is compiled here.
	"topBitSetTerminatedArray": {},
	"registryEntryHolder":      {},
	"registryEntryHolderSet":   {},
}

// nativeNames returns the set of names the schema declares native.
//
// A name is native only when its definition is the native marker itself. That
// is stricter than "the definition parses as a native node": `string` is
// defined as an invocation of the native `pstring`, so its stored node is a
// native node carrying the name `pstring`. Counting that as declaring `string`
// native would restore the bug this file removes, one name further along.
func nativeNames(schema *protodef.Schema) map[string]struct{} {
	names := make(map[string]struct{})
	if schema == nil {
		return names
	}
	for name, node := range schema.Types {
		if node != nil && node.Kind == protodef.KindNative && node.Name == name {
			names[name] = struct{}{}
		}
	}

	return names
}

// checkNativeCodecs reports a hand-written codec whose name the schema defines
// as something other than native.
//
// A codec the schema never mentions is merely unused, which is normal: one
// table serves every protocol version, and no version uses all of it. A codec
// whose name the schema *defines* is the dangerous case, because that is a
// type with a real definition and a competing hand-written implementation. It
// is the exact shape of the bug this file exists to prevent, so it fails
// generation rather than being resolved silently in either direction.
func checkNativeCodecs(schema *protodef.Schema, natives map[string]struct{}) error {
	if schema == nil {
		return nil
	}

	var shadowed []string
	for name := range nativeCodecs {
		if _, native := natives[name]; native {
			continue
		}
		if _, defined := schema.Types[name]; defined {
			shadowed = append(shadowed, name)
		}
	}
	if len(shadowed) == 0 {
		return nil
	}
	sort.Strings(shadowed)

	return fmt.Errorf("%w: %v", ErrDeadNativeCodec, shadowed)
}

// nativeRule resolves the codec for a name, and only for a name this schema
// declares native.
//
// The custom_payload exception is a field-level rule rather than a type-level
// one: the schema says restBuffer, and the plugin-payload codec bounds what an
// arbitrary channel may send. It is keyed by packet and field precisely so it
// cannot leak onto every restBuffer in the protocol.
func nativeRule(
	natives map[string]struct{},
	node *protodef.TypeNode,
	packetName, fieldName string,
	path string,
) (scalarRule, bool, error) {
	if node == nil {
		return scalarRule{}, false, nil
	}
	name, arguments := node.Name, node.Arguments
	if name == "" {
		return scalarRule{}, false, nil
	}
	if rule, ok := basicScalarRules[name]; ok {
		if err := rejectArguments(name, arguments, path); err != nil {
			return scalarRule{}, false, err
		}

		return rule, true, nil
	}
	if _, native := natives[name]; !native {
		return scalarRule{}, false, nil
	}
	if _, structural := structuralNatives[name]; structural {
		return scalarRule{}, false, nil
	}

	rule, ok := nativeCodecs[name]
	if !ok {
		return scalarRule{}, false, modelError(path, "no hand-written codec for native %q", name)
	}

	switch name {
	case "pstring":
		if err := requireVarIntCount(name, node, arguments, path); err != nil {
			return scalarRule{}, false, err
		}
	case "restBuffer":
		if err := rejectArguments(name, arguments, path); err != nil {
			return scalarRule{}, false, err
		}
		if packetName == "custom_payload" && fieldName == "data" {
			rule = scalarRule{goType: "[]byte", read: "ReadPluginPayload", write: "WritePluginPayload"}
		}
	default:
		if err := rejectArguments(name, arguments, path); err != nil {
			return scalarRule{}, false, err
		}
	}

	return rule, true, nil
}

// requireVarIntCount accepts the one argument shape ReadString implements. A
// pstring with a different count type is a different wire format, and quietly
// reading it as a VarInt-prefixed string would be wrong bytes.
// requireVarIntCount rejects a pstring whose length prefix is not a VarInt.
//
// The parser turns a countType into a Count rather than into an argument, so
// checking arguments alone would let `["pstring",{"countType":"i16"}]` through
// and read it with the VarInt-prefixed codec. That is a different wire format
// and a silently wrong decode, which is the whole failure mode this file is
// about.
func requireVarIntCount(name string, node *protodef.TypeNode, arguments []protodef.Argument, path string) error {
	if node.Count == nil {
		return nil
	}
	if node.Count.Kind == protodef.CountType && node.Count.Type != nil && node.Count.Type.Name != "varint" {
		return modelError(
			path,
			"%w: native %q supports countType varint, not %q",
			ErrUnknownNativeArgument,
			name,
			node.Count.Type.Name,
		)
	}
	if node.Count.Kind == protodef.CountFixed {
		return modelError(
			path,
			"%w: native %q does not support a fixed count",
			ErrUnknownNativeArgument,
			name,
		)
	}
	for _, argument := range arguments {
		if argument.Name != "countType" {
			return modelError(
				path,
				"%w: native %q does not accept argument %q",
				ErrUnknownNativeArgument,
				name,
				argument.Name,
			)
		}
		if argument.String != "varint" {
			return modelError(
				path,
				"%w: native %q supports countType varint, not %q",
				ErrUnknownNativeArgument,
				name,
				argument.String,
			)
		}
	}

	return nil
}

func rejectArguments(name string, arguments []protodef.Argument, path string) error {
	if len(arguments) == 0 {
		return nil
	}

	return modelError(
		path,
		"%w: native %q accepts no arguments, got %q",
		ErrUnknownNativeArgument,
		name,
		arguments[0].Name,
	)
}
