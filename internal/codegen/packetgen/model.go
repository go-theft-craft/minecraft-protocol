// Package packetgen builds the deterministic model used to render Java packet codecs.
package packetgen

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
)

// Options controls packet model construction and subsequent source generation.
type Options struct {
	PackageName string
}

// Model is the complete ordered input for packet source rendering.
type Model struct {
	PackageName  string
	States       []State
	Declarations []Declaration
	Factories    []Factory
	Mappers      []Mapper
}

// State is one ordered protocol state.
type State struct {
	SourceName string
	GoName     string
	Directions []Direction
}

// Direction is one ordered packet direction within a state.
type Direction struct {
	SourceName    string
	GoName        string
	ProtocolValue string
	Packets       []Packet
}

// Packet describes one concrete generated packet type and its codec operations.
type Packet struct {
	ID           int32
	SourceID     string
	SourceName   string
	SourceType   string
	GoName       string
	Path         string
	Fields       []Field
	Declarations []Declaration
	Decode       []Operation
	Encode       []Operation
}

// Field describes one concrete Go struct field.
type Field struct {
	SourceName string
	GoName     string
	GoType     string
	Path       string
	Anonymous  bool
	Mapper     string
}

// DeclarationKind identifies a generated nested Go declaration.
type DeclarationKind string

const (
	// DeclarationStruct is a nested container struct.
	DeclarationStruct DeclarationKind = "struct"
	// DeclarationSwitch is a switch union struct.
	DeclarationSwitch DeclarationKind = "switch"
	// DeclarationBitField is an unpacked bitfield struct.
	DeclarationBitField DeclarationKind = "bitfield"
	// DeclarationBitFlags is an unpacked bitflags struct.
	DeclarationBitFlags DeclarationKind = "bitflags"
)

// Declaration describes one nested generated Go type.
type Declaration struct {
	Name     string
	Kind     DeclarationKind
	Fields   []Field
	Switch   Switch
	BitField BitField
	BitFlags BitFlags
}

// Switch describes the cases represented by a switch union declaration.
type Switch struct {
	CompareTo   string
	CompareType string
	Cases       []SwitchCase
	HasDefault  bool
	Default     SwitchCase
}

// SwitchCase describes one field in a generated switch union.
type SwitchCase struct {
	SourceKey string
	Match     string
	Void      bool
	HasField  bool
	Field     Field
}

// BitField describes the wire integer and unpacked fields of a bitfield declaration.
type BitField struct {
	WireGoType  string
	ReadMethod  string
	WriteMethod string
	Fields      []BitFieldField
}

// BitFieldField describes one ordered bitfield segment.
type BitFieldField struct {
	SourceName string
	GoName     string
	GoType     string
	Size       int
	Signed     bool
	Shift      int
	Mask       string
	Path       string
}

// BitFlags describes the underlying wire integer and named boolean flags.
type BitFlags struct {
	WireGoType  string
	ReadMethod  string
	WriteMethod string
	Fields      []BitFlagField
}

// BitFlagField describes one named boolean bit.
type BitFlagField struct {
	SourceName string
	GoName     string
	Bit        int
	Mask       string
	Path       string
}

// OperationKind identifies one explicit decode or encode operation.
type OperationKind string

const (
	// OpValue calls one typed java.Buffer value method.
	OpValue OperationKind = "value"
	// OpMapper calls a typed java.Buffer method and performs a mapper lookup.
	OpMapper OperationKind = "mapper"
	// OpContainer evaluates ordered operations for a nested struct.
	OpContainer OperationKind = "container"
	// OpArray validates or reads a count and evaluates one element operation.
	OpArray OperationKind = "array"
	// OpOption reads or writes a presence boolean before its value operation.
	OpOption OperationKind = "option"
	// OpSwitch evaluates cases against a resolved field reference.
	OpSwitch OperationKind = "switch"
	// OpBitField reads or writes an explicitly described packed integer.
	OpBitField OperationKind = "bitfield"
	// OpBitFlags reads or writes an explicitly described flag integer.
	OpBitFlags OperationKind = "bitflags"
	// OpVoid performs no wire I/O.
	OpVoid OperationKind = "void"
)

// Operation is one node in an ordered decode or encode operation tree.
// Value expressions assume the generated method binds its packet as packet.
type Operation struct {
	Kind        OperationKind
	Method      string
	Value       string
	GoType      string
	WireGoType  string
	Path        string
	Declaration string
	Mapper      string
	Index       string
	Count       Count
	Compare     FieldReference
	Operations  []Operation
	Cases       []OperationCase
	HasDefault  bool
	Default     OperationCase
}

// OperationCase is one ordered switch branch and its explicit operations.
type OperationCase struct {
	SourceKey  string
	Match      string
	Void       bool
	Operations []Operation
}

// CountKind identifies how a generated collection obtains its count.
type CountKind string

const (
	// CountNone means the operation has no collection count.
	CountNone CountKind = ""
	// CountFixed is a literal count.
	CountFixed CountKind = "fixed"
	// CountReference reads a preceding generated field.
	CountReference CountKind = "reference"
	// CountType uses a typed wire count prefix.
	CountType CountKind = "type"
)

// Count contains all data required to render one collection count.
type Count struct {
	Kind           CountKind
	Fixed          int
	Reference      FieldReference
	WireGoType     string
	ReadMethod     string
	WriteMethod    string
	ValidateMethod string
}

// FieldReference is a resolved source reference and exact generated Go expression.
type FieldReference struct {
	Source string
	Value  string
	Path   string
	GoType string
	Mapper string
}

// Factory describes one exact packet constructor lookup entry.
type Factory struct {
	State          string
	StateValue     string
	Direction      string
	DirectionValue string
	ID             int32
	SourceID       string
	PacketName     string
	PacketType     string
}

// Mapper describes deterministic forward and reverse lookup tables.
type Mapper struct {
	Name        string
	ReadTable   string
	WriteTable  string
	WireGoType  string
	ReadMethod  string
	WriteMethod string
	Path        string
	Entries     []MapperEntry
}

// MapperEntry retains one source key, normalized wire value, and symbolic value.
type MapperEntry struct {
	SourceKey string
	WireValue string
	Symbol    string
}

type builder struct {
	model       *Model
	typeNames   *nameAllocator
	mapperNames *nameAllocator
	arrayIndex  int
	packet      *Packet
	packetName  string
	packetPath  string
}

type nameAllocator struct {
	used map[string]int
}

type scope struct {
	parent   *scope
	bindings map[string]FieldReference
}

type compiledValue struct {
	goType  string
	mapper  string
	void    bool
	decode  Operation
	encode  Operation
	visible map[string]FieldReference
}

type scalarRule struct {
	goType string
	read   string
	write  string
	bits   int
	signed bool
}

// Build converts a parsed ProtoDef schema into one deterministic renderer-ready model.
func Build(schema *protodef.Schema, options Options) (*Model, error) {
	if schema == nil {
		return nil, fmt.Errorf("packetgen: nil ProtoDef schema")
	}
	b := &builder{
		model:       &Model{PackageName: options.PackageName},
		typeNames:   newNameAllocator(),
		mapperNames: newNameAllocator(),
	}
	for _, sourceState := range schema.States {
		state := State{SourceName: sourceState.Name, GoName: exportedName(sourceState.Name)}
		for _, sourceDirection := range sourceState.Directions {
			direction, err := b.buildDirection(sourceState.Name, sourceDirection)
			if err != nil {
				return nil, err
			}
			state.Directions = append(state.Directions, direction)
		}
		b.model.States = append(b.model.States, state)
	}
	return b.model, nil
}

func (b *builder) buildDirection(stateName string, source protodef.Direction) (Direction, error) {
	directionName, protocolValue, err := directionNames(source.Name)
	if err != nil {
		return Direction{}, fmt.Errorf("packetgen: %s.%s: %w", stateName, source.Name, err)
	}
	direction := Direction{
		SourceName:    source.Name,
		GoName:        directionName,
		ProtocolValue: protocolValue,
	}
	for _, sourcePacket := range source.Packets {
		packet, buildErr := b.buildPacket(stateName, source.Name, directionName, sourcePacket)
		if buildErr != nil {
			return Direction{}, buildErr
		}
		direction.Packets = append(direction.Packets, packet)
		b.model.Declarations = append(b.model.Declarations, packet.Declarations...)
		b.model.Factories = append(b.model.Factories, Factory{
			State:          stateName,
			StateValue:     strconv.Quote(stateName),
			Direction:      source.Name,
			DirectionValue: protocolValue,
			ID:             packet.ID,
			SourceID:       packet.SourceID,
			PacketName:     packet.SourceName,
			PacketType:     packet.GoName,
		})
	}
	return direction, nil
}

func (b *builder) buildPacket(stateName, directionName, directionGoName string, source protodef.Packet) (Packet, error) {
	packetName := b.typeNames.allocate(exportedName(stateName) + directionGoName + exportedName(source.Name))
	packetPath := stateName + "." + directionName + "." + source.Name
	packet := Packet{
		ID:         int32(source.ID),
		SourceID:   source.IDKey,
		SourceName: source.Name,
		SourceType: source.TypeName,
		GoName:     packetName,
		Path:       packetPath,
	}

	node := source.Type
	for node != nil && node.Kind == protodef.KindAlias && len(node.Arguments) == 0 {
		node = node.Target
	}
	if node == nil || node.Kind != protodef.KindContainer {
		return Packet{}, modelError(packetPath, "packet body must resolve to a container")
	}

	b.packet = &packet
	b.packetName = source.Name
	b.packetPath = packetPath
	b.arrayIndex = 0
	fields, decode, encode, _, err := b.compileContainerFields(node, packetName, packetPath, "packet", nil)
	if err != nil {
		return Packet{}, err
	}
	packet.Fields = fields
	packet.Decode = decode
	packet.Encode = encode
	return packet, nil
}

func (b *builder) compileContainerFields(
	node *protodef.TypeNode,
	owner string,
	path string,
	value string,
	parent *scope,
) ([]Field, []Operation, []Operation, map[string]FieldReference, error) {
	current := &scope{parent: parent, bindings: make(map[string]FieldReference)}
	fieldNames := newNameAllocator()
	fields := make([]Field, 0, len(node.Fields))
	decode := make([]Operation, 0, len(node.Fields))
	encode := make([]Operation, 0, len(node.Fields))
	anonymousCounts := make(map[protodef.Kind]int)

	for _, sourceField := range node.Fields {
		var goName string
		fieldPath := path
		if sourceField.Name != "" {
			goName = fieldNames.allocate(exportedName(sourceField.Name))
			fieldPath += "." + sourceField.Name
		} else {
			anonymousCounts[sourceField.Type.Kind]++
			goName = fieldNames.allocate(anonymousName(sourceField.Type.Kind, anonymousCounts[sourceField.Type.Kind]))
			fieldPath += "." + lowerFirst(goName)
		}
		fieldValue := selector(value, goName)
		compiled, err := b.compileNode(sourceField.Type, owner+goName, fieldPath, fieldValue, current, sourceField.Name)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		field := Field{
			SourceName: sourceField.Name,
			GoName:     goName,
			GoType:     compiled.goType,
			Path:       fieldPath,
			Anonymous:  sourceField.Anonymous,
			Mapper:     compiled.mapper,
		}
		fields = append(fields, field)
		decode = append(decode, compiled.decode)
		encode = append(encode, compiled.encode)

		if sourceField.Name != "" {
			current.bindings[sourceField.Name] = FieldReference{
				Source: sourceField.Name,
				Value:  fieldValue,
				Path:   fieldPath,
				GoType: compiled.goType,
				Mapper: compiled.mapper,
			}
		} else if sourceField.Anonymous {
			for name, reference := range compiled.visible {
				current.bindings[name] = reference
			}
		}
	}

	return fields, decode, encode, current.bindings, nil
}

func (b *builder) compileNode(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
	fieldName string,
) (compiledValue, error) {
	if node == nil {
		return compiledValue{}, modelError(path, "nil type node")
	}
	if rule, ok := specialScalarRule(node.Name, b.packetName, fieldName); ok {
		if rule.read == "" {
			return compiledValue{
				goType: "struct{}",
				void:   true,
				decode: Operation{Kind: OpVoid, Value: value, GoType: "struct{}", Path: path},
				encode: Operation{Kind: OpVoid, Value: value, GoType: "struct{}", Path: path},
			}, nil
		}
		return compileScalar(rule, path, value), nil
	}

	switch node.Kind {
	case protodef.KindAlias:
		if len(node.Arguments) != 0 {
			return compiledValue{}, modelError(path, "unsupported parameterized alias %q", node.Name)
		}
		return b.compileNode(node.Target, desiredName, path, value, current, fieldName)
	case protodef.KindPrimitive, protodef.KindNative:
		return compiledValue{}, modelError(path, "unsupported native type %q", node.Name)
	case protodef.KindContainer:
		return b.compileContainer(node, desiredName, path, value, current)
	case protodef.KindArray:
		return b.compileArray(node, desiredName, path, value, current)
	case protodef.KindSwitch:
		return b.compileSwitch(node, desiredName, path, value, current)
	case protodef.KindOption:
		return b.compileOption(node, desiredName, path, value, current)
	case protodef.KindBuffer:
		return b.compileBuffer(node, path, value, current)
	case protodef.KindMapper:
		return b.compileMapper(node, desiredName, path, value)
	case protodef.KindBitField:
		return b.compileBitField(node, desiredName, path, value)
	case protodef.KindBitFlags:
		return b.compileBitFlags(node, desiredName, path, value)
	default:
		return compiledValue{}, modelError(path, "unsupported ProtoDef kind %q", node.Kind)
	}
}

func (b *builder) compileContainer(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
) (compiledValue, error) {
	name := b.typeNames.allocate(desiredName)
	fields, decode, encode, visible, err := b.compileContainerFields(node, name, path, value, current)
	if err != nil {
		return compiledValue{}, err
	}
	b.packet.Declarations = append(b.packet.Declarations, Declaration{Name: name, Kind: DeclarationStruct, Fields: fields})
	return compiledValue{
		goType:  name,
		decode:  Operation{Kind: OpContainer, Value: value, GoType: name, Path: path, Declaration: name, Operations: decode},
		encode:  Operation{Kind: OpContainer, Value: value, GoType: name, Path: path, Declaration: name, Operations: encode},
		visible: visible,
	}, nil
}

func (b *builder) compileArray(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
) (compiledValue, error) {
	count, err := b.compileCount(node.Count, current, path)
	if err != nil {
		return compiledValue{}, err
	}
	index := fmt.Sprintf("index%d", b.arrayIndex)
	b.arrayIndex++
	elementValue := fmt.Sprintf("(%s)[%s]", value, index)
	element, err := b.compileNode(node.Element, desiredName+"Item", path+"[]", elementValue, current, "")
	if err != nil {
		return compiledValue{}, err
	}
	goType := "[]" + element.goType
	if count.Kind == CountFixed {
		goType = fmt.Sprintf("[%d]%s", count.Fixed, element.goType)
	}
	decodeMethod := "ValidateCollection"
	encodeMethod := "ValidateCollection"
	if count.Kind == CountType {
		decodeMethod = count.ReadMethod
		encodeMethod = count.WriteMethod
	}
	return compiledValue{
		goType: goType,
		decode: Operation{
			Kind: OpArray, Method: decodeMethod, Value: value, GoType: goType, Path: path,
			Index: index, Count: count, Operations: []Operation{element.decode},
		},
		encode: Operation{
			Kind: OpArray, Method: encodeMethod, Value: value, GoType: goType, Path: path,
			Index: index, Count: count, Operations: []Operation{element.encode},
		},
	}, nil
}

func (b *builder) compileOption(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
) (compiledValue, error) {
	element, err := b.compileNode(node.Element, desiredName+"Value", path, dereference(value), current, "")
	if err != nil {
		return compiledValue{}, err
	}
	goType := "*" + element.goType
	return compiledValue{
		goType: goType,
		decode: Operation{Kind: OpOption, Method: "ReadBool", Value: value, GoType: goType, Path: path, Operations: []Operation{element.decode}},
		encode: Operation{Kind: OpOption, Method: "WriteBool", Value: value, GoType: goType, Path: path, Operations: []Operation{element.encode}},
	}, nil
}

func (b *builder) compileBuffer(node *protodef.TypeNode, path, value string, current *scope) (compiledValue, error) {
	count, err := b.compileCount(node.Count, current, path)
	if err != nil {
		return compiledValue{}, err
	}
	readMethod := "ReadBuffer"
	writeMethod := "WriteBuffer"
	if count.Kind == CountType {
		if count.ReadMethod != "ReadCollectionLength" {
			return compiledValue{}, modelError(path, "prefixed buffer count must use varint")
		}
		readMethod = "ReadByteArray"
		writeMethod = "WriteByteArray"
	}
	return compiledValue{
		goType: "[]byte",
		decode: Operation{Kind: OpValue, Method: readMethod, Value: value, GoType: "[]byte", Path: path, Count: count},
		encode: Operation{Kind: OpValue, Method: writeMethod, Value: value, GoType: "[]byte", Path: path, Count: count},
	}, nil
}

func (b *builder) compileMapper(node *protodef.TypeNode, desiredName, path, value string) (compiledValue, error) {
	rule, err := scalarRuleForNode(node.Element)
	if err != nil || rule.bits == 0 {
		return compiledValue{}, modelError(path, "mapper wire type is unsupported")
	}
	name := b.mapperNames.allocate(desiredName + "Mapper")
	mapper := Mapper{
		Name:        name,
		ReadTable:   lowerFirst(name) + "ByWire",
		WriteTable:  lowerFirst(name) + "ByName",
		WireGoType:  rule.goType,
		ReadMethod:  rule.read,
		WriteMethod: rule.write,
		Path:        path,
	}
	seenSymbols := make(map[string]struct{}, len(node.Mappings))
	seenWireValues := make(map[string]struct{}, len(node.Mappings))
	for _, mapping := range node.Mappings {
		wireValue, normalizeErr := normalizeInteger(mapping.Key, rule)
		if normalizeErr != nil {
			return compiledValue{}, modelError(path, "invalid mapper key %q for %s", mapping.Key, rule.goType)
		}
		if _, duplicate := seenWireValues[wireValue]; duplicate {
			return compiledValue{}, modelError(path, "duplicate mapper wire value %s", wireValue)
		}
		seenWireValues[wireValue] = struct{}{}
		if _, duplicate := seenSymbols[mapping.Value]; duplicate {
			return compiledValue{}, modelError(path, "duplicate mapper symbol %q", mapping.Value)
		}
		seenSymbols[mapping.Value] = struct{}{}
		mapper.Entries = append(mapper.Entries, MapperEntry{SourceKey: mapping.Key, WireValue: wireValue, Symbol: mapping.Value})
	}
	b.model.Mappers = append(b.model.Mappers, mapper)
	return compiledValue{
		goType: "string",
		mapper: name,
		decode: Operation{Kind: OpMapper, Method: rule.read, Value: value, GoType: "string", WireGoType: rule.goType, Path: path, Mapper: name},
		encode: Operation{Kind: OpMapper, Method: rule.write, Value: value, GoType: "string", WireGoType: rule.goType, Path: path, Mapper: name},
	}, nil
}

func (b *builder) compileSwitch(
	node *protodef.TypeNode,
	desiredName string,
	path string,
	value string,
	current *scope,
) (compiledValue, error) {
	compare, err := resolveReference(current, node.CompareTo)
	if err != nil {
		return compiledValue{}, modelError(path, "switch compareTo %q: %v", node.CompareTo, err)
	}
	name := b.typeNames.allocate(desiredName + "Switch")
	fieldNames := newNameAllocator()
	declaration := Declaration{
		Name:   name,
		Kind:   DeclarationSwitch,
		Switch: Switch{CompareTo: node.CompareTo, CompareType: compare.GoType},
	}
	decode := Operation{Kind: OpSwitch, Value: value, GoType: name, Path: path, Declaration: name, Compare: compare}
	encode := decode
	for _, sourceCase := range node.Cases {
		match, matchErr := switchMatch(sourceCase.Key, compare)
		if matchErr != nil {
			return compiledValue{}, modelError(path, "invalid switch case %q for %s", sourceCase.Key, compare.GoType)
		}
		declCase, decodeCase, encodeCase, compileErr := b.compileSwitchCase(
			sourceCase.Key,
			match,
			sourceCase.Type,
			fieldNames,
			name,
			path,
			value,
			current,
		)
		if compileErr != nil {
			return compiledValue{}, compileErr
		}
		declaration.Switch.Cases = append(declaration.Switch.Cases, declCase)
		declaration.Fields = appendCaseField(declaration.Fields, declCase)
		decode.Cases = append(decode.Cases, decodeCase)
		encode.Cases = append(encode.Cases, encodeCase)
	}
	if node.Default != nil {
		declCase, decodeCase, encodeCase, compileErr := b.compileSwitchCase(
			"default", "", node.Default, fieldNames, name, path, value, current,
		)
		if compileErr != nil {
			return compiledValue{}, compileErr
		}
		declaration.Switch.HasDefault = true
		declaration.Switch.Default = declCase
		declaration.Fields = appendCaseField(declaration.Fields, declCase)
		decode.HasDefault = true
		decode.Default = decodeCase
		encode.HasDefault = true
		encode.Default = encodeCase
	}
	b.packet.Declarations = append(b.packet.Declarations, declaration)
	return compiledValue{goType: name, decode: decode, encode: encode}, nil
}

func (b *builder) compileSwitchCase(
	sourceKey string,
	match string,
	node *protodef.TypeNode,
	fieldNames *nameAllocator,
	owner string,
	path string,
	value string,
	current *scope,
) (SwitchCase, OperationCase, OperationCase, error) {
	goName := fieldNames.allocate(caseName(sourceKey))
	caseValue := selector(value, goName)
	compiled, err := b.compileNode(node, owner+goName, path, caseValue, current, "")
	if err != nil {
		return SwitchCase{}, OperationCase{}, OperationCase{}, err
	}
	declCase := SwitchCase{SourceKey: sourceKey, Match: match, Void: compiled.void}
	decodeCase := OperationCase{SourceKey: sourceKey, Match: match, Void: compiled.void}
	encodeCase := decodeCase
	if !compiled.void {
		declCase.HasField = true
		declCase.Field = Field{SourceName: sourceKey, GoName: goName, GoType: compiled.goType, Path: path, Mapper: compiled.mapper}
		decodeCase.Operations = []Operation{compiled.decode}
		encodeCase.Operations = []Operation{compiled.encode}
	}
	return declCase, decodeCase, encodeCase, nil
}

func (b *builder) compileBitField(node *protodef.TypeNode, desiredName, path, value string) (compiledValue, error) {
	total := 0
	for _, field := range node.Bits {
		total += field.Size
	}
	rule, ok := unsignedRuleForBits(total)
	if !ok {
		return compiledValue{}, modelError(path, "bitfield width %d has no java.Buffer method", total)
	}
	name := b.typeNames.allocate(desiredName + "Bits")
	fieldNames := newNameAllocator()
	declaration := Declaration{
		Name:     name,
		Kind:     DeclarationBitField,
		BitField: BitField{WireGoType: rule.goType, ReadMethod: rule.read, WriteMethod: rule.write},
	}
	visible := make(map[string]FieldReference, len(node.Bits))
	offset := 0
	for _, sourceField := range node.Bits {
		goName := fieldNames.allocate(exportedName(sourceField.Name))
		fieldPath := path + "." + sourceField.Name
		field := BitFieldField{
			SourceName: sourceField.Name,
			GoName:     goName,
			GoType:     integerGoType(sourceField.Size, sourceField.Signed),
			Size:       sourceField.Size,
			Signed:     sourceField.Signed,
			Shift:      total - offset - sourceField.Size,
			Mask:       integerMask(sourceField.Size),
			Path:       fieldPath,
		}
		offset += sourceField.Size
		declaration.BitField.Fields = append(declaration.BitField.Fields, field)
		declaration.Fields = append(declaration.Fields, Field{SourceName: sourceField.Name, GoName: goName, GoType: field.GoType, Path: fieldPath})
		visible[sourceField.Name] = FieldReference{Source: sourceField.Name, Value: selector(value, goName), Path: fieldPath, GoType: field.GoType}
	}
	b.packet.Declarations = append(b.packet.Declarations, declaration)
	return compiledValue{
		goType:  name,
		decode:  Operation{Kind: OpBitField, Method: rule.read, Value: value, GoType: name, WireGoType: rule.goType, Path: path, Declaration: name},
		encode:  Operation{Kind: OpBitField, Method: rule.write, Value: value, GoType: name, WireGoType: rule.goType, Path: path, Declaration: name},
		visible: visible,
	}, nil
}

func (b *builder) compileBitFlags(node *protodef.TypeNode, desiredName, path, value string) (compiledValue, error) {
	rule, err := scalarRuleForNode(node.Element)
	if err != nil || rule.bits == 0 || rule.signed {
		return compiledValue{}, modelError(path, "bitflags wire type is unsupported")
	}
	if len(node.Flags) > rule.bits {
		return compiledValue{}, modelError(path, "%d flags exceed %d-bit wire type", len(node.Flags), rule.bits)
	}
	name := b.typeNames.allocate(desiredName + "Flags")
	fieldNames := newNameAllocator()
	declaration := Declaration{
		Name:     name,
		Kind:     DeclarationBitFlags,
		BitFlags: BitFlags{WireGoType: rule.goType, ReadMethod: rule.read, WriteMethod: rule.write},
	}
	for bit, sourceName := range node.Flags {
		goName := fieldNames.allocate(exportedName(sourceName))
		fieldPath := path + "." + sourceName
		field := BitFlagField{SourceName: sourceName, GoName: goName, Bit: bit, Mask: integerMaskAt(bit), Path: fieldPath}
		declaration.BitFlags.Fields = append(declaration.BitFlags.Fields, field)
		declaration.Fields = append(declaration.Fields, Field{SourceName: sourceName, GoName: goName, GoType: "bool", Path: fieldPath})
	}
	b.packet.Declarations = append(b.packet.Declarations, declaration)
	return compiledValue{
		goType: name,
		decode: Operation{Kind: OpBitFlags, Method: rule.read, Value: value, GoType: name, WireGoType: rule.goType, Path: path, Declaration: name},
		encode: Operation{Kind: OpBitFlags, Method: rule.write, Value: value, GoType: name, WireGoType: rule.goType, Path: path, Declaration: name},
	}, nil
}

func (b *builder) compileCount(source *protodef.Count, current *scope, path string) (Count, error) {
	if source == nil {
		return Count{}, modelError(path, "missing collection count")
	}
	switch source.Kind {
	case protodef.CountFixed:
		return Count{Kind: CountFixed, Fixed: source.Fixed, ValidateMethod: "ValidateCollection"}, nil
	case protodef.CountReference:
		reference, err := resolveReference(current, source.Reference)
		if err != nil {
			return Count{}, modelError(path, "count reference %q: %v", source.Reference, err)
		}
		if !isIntegerType(reference.GoType) {
			return Count{}, modelError(path, "count reference %q has non-integer Go type %s", source.Reference, reference.GoType)
		}
		return Count{Kind: CountReference, Reference: reference, ValidateMethod: "ValidateCollection"}, nil
	case protodef.CountType:
		rule, err := scalarRuleForNode(source.Type)
		if err != nil || rule.bits == 0 {
			return Count{}, modelError(path, "count type must use an integer java.Buffer method")
		}
		readMethod := rule.read
		writeMethod := rule.write
		if rule.read == "ReadVarInt" {
			readMethod = "ReadCollectionLength"
			writeMethod = "WriteCollectionLength"
		}
		return Count{
			Kind: CountType, WireGoType: rule.goType, ReadMethod: readMethod, WriteMethod: writeMethod,
			ValidateMethod: "ValidateCollection",
		}, nil
	default:
		return Count{}, modelError(path, "unsupported count kind %q", source.Kind)
	}
}

func compileScalar(rule scalarRule, path, value string) compiledValue {
	return compiledValue{
		goType: rule.goType,
		decode: Operation{Kind: OpValue, Method: rule.read, Value: value, GoType: rule.goType, WireGoType: rule.goType, Path: path},
		encode: Operation{Kind: OpValue, Method: rule.write, Value: value, GoType: rule.goType, WireGoType: rule.goType, Path: path},
	}
}

func scalarRuleForNode(node *protodef.TypeNode) (scalarRule, error) {
	seen := make(map[*protodef.TypeNode]struct{})
	for node != nil {
		if _, duplicate := seen[node]; duplicate {
			return scalarRule{}, fmt.Errorf("alias cycle")
		}
		seen[node] = struct{}{}
		if rule, ok := basicScalarRules[node.Name]; ok {
			return rule, nil
		}
		if node.Kind != protodef.KindAlias || len(node.Arguments) != 0 {
			break
		}
		node = node.Target
	}
	return scalarRule{}, fmt.Errorf("unsupported scalar")
}

var basicScalarRules = map[string]scalarRule{
	"varint":  {goType: "int32", read: "ReadVarInt", write: "WriteVarInt", bits: 32, signed: true},
	"varlong": {goType: "int64", read: "ReadVarLong", write: "WriteVarLong", bits: 64, signed: true},
	"i8":      {goType: "int8", read: "ReadI8", write: "WriteI8", bits: 8, signed: true},
	"u8":      {goType: "uint8", read: "ReadU8", write: "WriteU8", bits: 8},
	"i16":     {goType: "int16", read: "ReadI16", write: "WriteI16", bits: 16, signed: true},
	"u16":     {goType: "uint16", read: "ReadU16", write: "WriteU16", bits: 16},
	"i32":     {goType: "int32", read: "ReadI32", write: "WriteI32", bits: 32, signed: true},
	"u32":     {goType: "uint32", read: "ReadU32", write: "WriteU32", bits: 32},
	"i64":     {goType: "int64", read: "ReadI64", write: "WriteI64", bits: 64, signed: true},
	"u64":     {goType: "uint64", read: "ReadU64", write: "WriteU64", bits: 64},
	"f32":     {goType: "float32", read: "ReadF32", write: "WriteF32"},
	"f64":     {goType: "float64", read: "ReadF64", write: "WriteF64"},
	"bool":    {goType: "bool", read: "ReadBool", write: "WriteBool"},
}

func specialScalarRule(name, packetName, fieldName string) (scalarRule, bool) {
	if rule, ok := basicScalarRules[name]; ok {
		return rule, true
	}
	switch name {
	case "UUID":
		return scalarRule{goType: "java.UUID", read: "ReadUUID", write: "WriteUUID"}, true
	case "string", "pstring":
		return scalarRule{goType: "string", read: "ReadString", write: "WriteString"}, true
	case "ByteArray":
		return scalarRule{goType: "[]byte", read: "ReadByteArray", write: "WriteByteArray"}, true
	case "restBuffer":
		if packetName == "custom_payload" && fieldName == "data" {
			return scalarRule{goType: "[]byte", read: "ReadPluginPayload", write: "WritePluginPayload"}, true
		}
		return scalarRule{goType: "[]byte", read: "ReadRestBuffer", write: "WriteRestBuffer"}, true
	case "nbt":
		return scalarRule{goType: "java.NBT", read: "ReadNBT", write: "WriteNBT"}, true
	case "optionalNbt":
		return scalarRule{goType: "*java.NBT", read: "ReadOptionalNBT", write: "WriteOptionalNBT"}, true
	case "slot":
		return scalarRule{goType: "java.Slot", read: "ReadSlot", write: "WriteSlot"}, true
	case "entityMetadata", "entityMetadataLoop":
		return scalarRule{goType: "java.EntityMetadata", read: "ReadEntityMetadata", write: "WriteEntityMetadata"}, true
	case "position":
		return scalarRule{goType: "java.Position", read: "ReadPosition", write: "WritePosition"}, true
	case "void":
		return scalarRule{}, true
	default:
		return scalarRule{}, false
	}
}

func unsignedRuleForBits(bits int) (scalarRule, bool) {
	for _, name := range []string{"u8", "u16", "u32", "u64"} {
		rule := basicScalarRules[name]
		if rule.bits == bits {
			return rule, true
		}
	}
	return scalarRule{}, false
}

func resolveReference(current *scope, source string) (FieldReference, error) {
	if strings.HasPrefix(source, "$") {
		return FieldReference{}, fmt.Errorf("parameter reference is not bound")
	}
	for strings.HasPrefix(source, "../") {
		if current == nil || current.parent == nil {
			return FieldReference{}, fmt.Errorf("reference escapes the packet")
		}
		current = current.parent
		source = strings.TrimPrefix(source, "../")
	}
	if current == nil || source == "" {
		return FieldReference{}, fmt.Errorf("reference is empty")
	}
	if strings.ContainsRune(source, '/') {
		return FieldReference{}, fmt.Errorf("nested field references are unsupported")
	}
	name := source
	reference, ok := current.bindings[name]
	if !ok {
		return FieldReference{}, fmt.Errorf("field %q is not available", name)
	}
	reference.Source = source
	return reference, nil
}

func switchMatch(source string, compare FieldReference) (string, error) {
	if compare.Mapper != "" || compare.GoType == "string" {
		return strconv.Quote(source), nil
	}
	if compare.GoType == "bool" {
		if source != "true" && source != "false" {
			return "", fmt.Errorf("invalid boolean")
		}
		return source, nil
	}
	rule, ok := ruleForGoType(compare.GoType)
	if !ok || rule.bits == 0 {
		return "", fmt.Errorf("unsupported comparison type")
	}
	return normalizeInteger(source, rule)
}

func normalizeInteger(source string, rule scalarRule) (string, error) {
	base := 10
	if strings.HasPrefix(source, "0x") || strings.HasPrefix(source, "-0x") {
		base = 0
	}
	if rule.signed {
		value, err := strconv.ParseInt(source, base, rule.bits)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(value, 10), nil
	}
	value, err := strconv.ParseUint(source, base, rule.bits)
	if err != nil {
		return "", err
	}
	return strconv.FormatUint(value, 10), nil
}

func ruleForGoType(goType string) (scalarRule, bool) {
	for _, rule := range basicScalarRules {
		if rule.goType == goType {
			return rule, true
		}
	}
	return scalarRule{}, false
}

func directionNames(source string) (string, string, error) {
	switch source {
	case "toClient":
		return "Clientbound", "protocol.DirectionClientbound", nil
	case "toServer":
		return "Serverbound", "protocol.DirectionServerbound", nil
	default:
		return "", "", fmt.Errorf("unsupported direction")
	}
}

func appendCaseField(fields []Field, source SwitchCase) []Field {
	if source.HasField {
		return append(fields, source.Field)
	}
	return fields
}

func newNameAllocator() *nameAllocator {
	return &nameAllocator{used: make(map[string]int)}
}

func (a *nameAllocator) allocate(base string) string {
	if base == "" {
		base = "Value"
	}
	count := a.used[base]
	if count == 0 {
		a.used[base] = 1
		return base
	}
	for {
		count++
		candidate := base + strconv.Itoa(count)
		if a.used[candidate] == 0 {
			a.used[base] = count
			a.used[candidate] = 1
			return candidate
		}
	}
}

func exportedName(source string) string {
	words := splitWords(source)
	var result strings.Builder
	for _, word := range words {
		lower := strings.ToLower(word)
		switch lower {
		case "id", "uuid", "nbt", "url", "ip":
			result.WriteString(strings.ToUpper(lower))
		default:
			runes := []rune(lower)
			if len(runes) != 0 {
				runes[0] = unicode.ToUpper(runes[0])
				result.WriteString(string(runes))
			}
		}
	}
	name := result.String()
	if name != "" && unicode.IsDigit([]rune(name)[0]) {
		return "Value" + name
	}
	return name
}

func splitWords(source string) []string {
	runes := []rune(source)
	words := make([]string, 0, 4)
	start := -1
	for index, current := range runes {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			if start >= 0 {
				words = append(words, string(runes[start:index]))
				start = -1
			}
			continue
		}
		if start < 0 {
			start = index
			continue
		}
		previous := runes[index-1]
		nextLower := index+1 < len(runes) && unicode.IsLower(runes[index+1])
		if unicode.IsUpper(current) && (unicode.IsLower(previous) || unicode.IsDigit(previous) || unicode.IsUpper(previous) && nextLower) {
			words = append(words, string(runes[start:index]))
			start = index
		}
	}
	if start >= 0 {
		words = append(words, string(runes[start:]))
	}
	return words
}

func anonymousName(kind protodef.Kind, number int) string {
	name := "AnonymousField"
	switch kind {
	case protodef.KindContainer:
		name = "AnonymousContainer"
	case protodef.KindSwitch:
		name = "AnonymousSwitch"
	case protodef.KindBitField:
		name = "AnonymousBitField"
	case protodef.KindBitFlags:
		name = "AnonymousBitFlags"
	case protodef.KindAlias,
		protodef.KindPrimitive,
		protodef.KindNative,
		protodef.KindArray,
		protodef.KindOption,
		protodef.KindBuffer,
		protodef.KindMapper:
	}
	return name + strconv.Itoa(number)
}

func caseName(source string) string {
	if source == "default" {
		return "Default"
	}
	if value, err := strconv.ParseInt(source, 0, 64); err == nil {
		if value < 0 {
			return "CaseNegative" + strconv.FormatInt(-value, 10)
		}
		return "Case" + strconv.FormatInt(value, 10)
	}
	name := exportedName(source)
	if name == "" {
		return "Case"
	}
	return name
}

func selector(value, field string) string {
	if strings.HasPrefix(value, "*(") {
		return "(" + value + ")." + field
	}
	return value + "." + field
}

func dereference(value string) string {
	return "*(" + value + ")"
}

func lowerFirst(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return value
	}
	runes[0] = unicode.ToLower(runes[0])
	return string(runes)
}

func integerGoType(bits int, signed bool) string {
	width := 8
	for width < bits {
		width *= 2
	}
	if signed {
		return fmt.Sprintf("int%d", width)
	}
	return fmt.Sprintf("uint%d", width)
}

func integerMask(bits int) string {
	if bits == 64 {
		return "0xffffffffffffffff"
	}
	return fmt.Sprintf("0x%x", uint64(1)<<bits-1)
}

func integerMaskAt(bit int) string {
	return fmt.Sprintf("0x%x", uint64(1)<<bit)
}

func isIntegerType(goType string) bool {
	rule, ok := ruleForGoType(goType)
	return ok && rule.bits != 0
}

func modelError(path, format string, arguments ...any) error {
	return fmt.Errorf("packetgen: %s: %s", path, fmt.Sprintf(format, arguments...))
}
