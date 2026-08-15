package packetgen

import (
	"embed"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"text/template"
)

const (
	packetsFile    = "packets.go"
	codecFile      = "codec.go"
	descriptorFile = "descriptor.go"
)

//go:embed templates/*.tmpl
var generatorTemplates embed.FS

type generatorTemplateData struct {
	PackageName string
	Body        string
	ImportJava  bool
	HasPackets  bool
}

type sourceWriter struct {
	strings.Builder
	indent int
}

func (w *sourceWriter) line(format string, arguments ...any) {
	if format != "" {
		w.WriteString(strings.Repeat("\t", w.indent))
		_, _ = fmt.Fprintf(&w.Builder, format, arguments...)
	}
	w.WriteByte('\n')
}

type operationRenderer struct {
	declarations map[string]Declaration
	mappers      map[string]Mapper
	temporary    int
}

type logicalFactoryKey struct {
	state     string
	direction string
	id        int32
}

// Generate renders formatted packet declarations, direct codecs, and packet factories.
func Generate(model *Model, options Options) (map[string][]byte, error) {
	if model == nil {
		return nil, fmt.Errorf("packetgen: nil model")
	}
	packageName := options.PackageName
	if packageName == "" {
		packageName = model.PackageName
	}
	if packageName == "" {
		return nil, fmt.Errorf("packetgen: missing package name")
	}
	if !token.IsIdentifier(packageName) || token.Lookup(packageName).IsKeyword() {
		return nil, fmt.Errorf("packetgen: invalid package name %q", packageName)
	}
	if err := validateGenerationModel(model); err != nil {
		return nil, err
	}

	packetsBody, importJava := renderPackets(model)
	codecBody, err := renderCodecs(model)
	if err != nil {
		return nil, err
	}
	descriptorBody := renderDescriptor(model)
	hasPackets := modelPacketCount(model) != 0

	inputs := []struct {
		name string
		data generatorTemplateData
	}{
		{name: packetsFile, data: generatorTemplateData{PackageName: packageName, Body: packetsBody, ImportJava: importJava}},
		{name: codecFile, data: generatorTemplateData{PackageName: packageName, Body: codecBody, HasPackets: hasPackets}},
		{name: descriptorFile, data: generatorTemplateData{PackageName: packageName, Body: descriptorBody}},
	}
	files := make(map[string][]byte, len(inputs))
	for _, input := range inputs {
		source, renderErr := executeGeneratorTemplate(input.name, input.data)
		if renderErr != nil {
			return nil, renderErr
		}
		files[input.name] = source
	}
	return files, nil
}

func validateGenerationModel(model *Model) error {
	seenFactories := make(map[logicalFactoryKey]struct{}, len(model.Factories))
	for _, factory := range model.Factories {
		stateValue := factory.StateValue
		if stateValue == "" {
			stateValue = strconv.Quote(factory.State)
		}
		if _, err := parser.ParseExpr(stateValue); err != nil {
			return fmt.Errorf("packetgen: factory %s.%s: invalid state expression %q: %w", factory.State, factory.Direction, stateValue, err)
		}
		if _, err := parser.ParseExpr(factory.DirectionValue); err != nil {
			return fmt.Errorf(
				"packetgen: factory %s.%s: invalid direction expression %q: %w",
				factory.State,
				factory.Direction,
				factory.DirectionValue,
				err,
			)
		}
		key := logicalFactoryKey{state: factory.State, direction: factory.Direction, id: factory.ID}
		if _, duplicate := seenFactories[key]; duplicate {
			return fmt.Errorf(
				"packetgen: duplicate packet factory %s.%s.%s",
				factory.State,
				factory.Direction,
				packetIDLiteral(factory.SourceID, factory.ID),
			)
		}
		seenFactories[key] = struct{}{}
	}
	return nil
}

func executeGeneratorTemplate(name string, data generatorTemplateData) ([]byte, error) {
	path := "templates/" + name + ".tmpl"
	raw, err := generatorTemplates.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("packetgen: read template %s: %w", path, err)
	}
	parsed, err := template.New(name).Option("missingkey=error").Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("packetgen: parse template %s: %w", path, err)
	}
	var rendered strings.Builder
	if err := parsed.Execute(&rendered, data); err != nil {
		return nil, fmt.Errorf("packetgen: execute template %s: %w", path, err)
	}
	formatted, err := format.Source([]byte(rendered.String()))
	if err != nil {
		return nil, fmt.Errorf("packetgen: format %s: %w\n%s", name, err, rendered.String())
	}
	return formatted, nil
}

func renderPackets(model *Model) (string, bool) {
	var output sourceWriter
	importJava := false
	// A shared type whose definition did not produce its own declaration
	// still needs a named Go type to hang Decode and Encode on.
	for _, shared := range model.SharedTypes {
		if shared.GoType == shared.GoName {
			continue
		}
		output.line("type %s %s", shared.GoName, shared.GoType)
		output.line("")
		importJava = importJava || strings.Contains(shared.GoType, "java.")
	}
	for _, declaration := range model.Declarations {
		renderStruct(&output, declaration.Name, declaration.Fields)
		importJava = importJava || fieldsUseJava(declaration.Fields)
	}
	for _, state := range model.States {
		for _, direction := range state.Directions {
			for _, packet := range direction.Packets {
				renderStruct(&output, packet.GoName, packet.Fields)
				output.line("func (%s) PacketID() int32 { return %s }", packet.GoName, packetIDLiteral(packet.SourceID, packet.ID))
				output.line("")
				importJava = importJava || fieldsUseJava(packet.Fields)
			}
		}
	}
	return strings.TrimSpace(output.String()) + "\n", importJava
}

func renderStruct(output *sourceWriter, name string, fields []Field) {
	if len(fields) == 0 {
		output.line("type %s struct{}", name)
		output.line("")
		return
	}
	output.line("type %s struct {", name)
	output.indent++
	for _, field := range fields {
		output.line("%s %s", field.GoName, field.GoType)
	}
	output.indent--
	output.line("}")
	output.line("")
}

func fieldsUseJava(fields []Field) bool {
	for _, field := range fields {
		if strings.Contains(field.GoType, "java.") {
			return true
		}
	}
	return false
}

func renderCodecs(model *Model) (string, error) {
	declarations := make(map[string]Declaration, len(model.Declarations))
	for _, declaration := range model.Declarations {
		if _, duplicate := declarations[declaration.Name]; duplicate {
			return "", fmt.Errorf("packetgen: duplicate declaration %q", declaration.Name)
		}
		declarations[declaration.Name] = declaration
	}
	var output sourceWriter
	mappers := make(map[string]Mapper, len(model.Mappers))
	for _, mapper := range model.Mappers {
		if _, duplicate := mappers[mapper.Name]; duplicate {
			return "", fmt.Errorf("packetgen: duplicate mapper %q", mapper.Name)
		}
		mappers[mapper.Name] = mapper
		renderMapper(&output, mapper)
	}
	renderer := operationRenderer{declarations: declarations, mappers: mappers}
	for _, shared := range model.SharedTypes {
		if err := renderer.renderSharedCodec(&output, shared); err != nil {
			return "", err
		}
	}
	for _, state := range model.States {
		for _, direction := range state.Directions {
			for _, packet := range direction.Packets {
				if err := renderer.renderPacketCodec(&output, packet); err != nil {
					return "", err
				}
			}
		}
	}
	if output.Len() == 0 {
		return "", nil
	}
	return strings.TrimSpace(output.String()) + "\n", nil
}

func renderMapper(output *sourceWriter, mapper Mapper) {
	output.line("var %s = map[%s]string{", mapper.ReadTable, mapper.WireGoType)
	output.indent++
	for _, entry := range mapper.Entries {
		output.line("%s: %s,", entry.WireValue, strconv.Quote(entry.Symbol))
	}
	output.indent--
	output.line("}")
	output.line("")
	output.line("var %s = map[string]%s{", mapper.WriteTable, mapper.WireGoType)
	output.indent++
	for _, entry := range mapper.Entries {
		output.line("%s: %s,", strconv.Quote(entry.Symbol), entry.WireValue)
	}
	output.indent--
	output.line("}")
	output.line("")
}

// renderSharedCodec emits Decode and Encode for one shared named type.
//
// Decode counts nesting depth. A shared type is the only place a schema can be
// recursive, so it is the only place an unbounded peer input can drive
// unbounded recursion, and the counter is what turns that into a decode error
// instead of a stack overflow.
func (r *operationRenderer) renderSharedCodec(output *sourceWriter, shared SharedType) error {
	r.temporary = 0
	output.line("func (shared *%s) Decode(buffer *java.Buffer) error {", shared.GoName)
	output.indent++
	output.line("if shared == nil {")
	output.indent++
	output.line("return fmt.Errorf(%s)", strconv.Quote("decode "+shared.GoName+": nil value"))
	output.indent--
	output.line("}")
	output.line("if err := buffer.EnterNested(%s); err != nil {", strconv.Quote(shared.SchemaName))
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	output.line("defer buffer.LeaveNested()")
	for _, operation := range shared.Decode {
		if err := r.renderDecodeOperation(output, operation); err != nil {
			return generationPathError(operation.Path, err)
		}
	}
	output.line("return nil")
	output.indent--
	output.line("}")
	output.line("")

	r.temporary = 0
	output.line("func (shared *%s) Encode(buffer *java.Buffer) error {", shared.GoName)
	output.indent++
	output.line("if shared == nil {")
	output.indent++
	output.line("return fmt.Errorf(%s)", strconv.Quote("encode "+shared.GoName+": nil value"))
	output.indent--
	output.line("}")
	for _, operation := range shared.Encode {
		if err := r.renderEncodeOperation(output, operation); err != nil {
			return generationPathError(operation.Path, err)
		}
	}
	output.line("return nil")
	output.indent--
	output.line("}")
	output.line("")

	return nil
}

// renderShared delegates to a shared type's own codec.
func (r *operationRenderer) renderShared(output *sourceWriter, operation Operation, method string) error {
	output.line("if err := (&%s).%s(buffer); err != nil {", operation.Value, method)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")

	return nil
}

func (r *operationRenderer) renderPacketCodec(output *sourceWriter, packet Packet) error {
	r.temporary = 0
	output.line("func (packet *%s) Decode(buffer *java.Buffer) error {", packet.GoName)
	output.indent++
	output.line("if packet == nil {")
	output.indent++
	output.line("return fmt.Errorf(%s)", strconv.Quote("decode "+packet.GoName+": nil packet"))
	output.indent--
	output.line("}")
	output.line("target := packet")
	output.line("packet = new(%s)", packet.GoName)
	for _, operation := range packet.Decode {
		if err := r.renderDecodeOperation(output, operation); err != nil {
			return generationPathError(operation.Path, err)
		}
	}
	output.line("*target = *packet")
	output.line("return nil")
	output.indent--
	output.line("}")
	output.line("")

	r.temporary = 0
	output.line("func (packet *%s) Encode(buffer *java.Buffer) error {", packet.GoName)
	output.indent++
	output.line("if packet == nil {")
	output.indent++
	output.line("return fmt.Errorf(%s)", strconv.Quote("encode "+packet.GoName+": nil packet"))
	output.indent--
	output.line("}")
	for _, operation := range packet.Encode {
		if err := r.renderEncodeOperation(output, operation); err != nil {
			return generationPathError(operation.Path, err)
		}
	}
	output.line("return nil")
	output.indent--
	output.line("}")
	output.line("")
	return nil
}

func (r *operationRenderer) renderDecodeOperation(output *sourceWriter, operation Operation) error {
	switch operation.Kind {
	case OpValue:
		return r.renderDecodeValue(output, operation)
	case OpMapper:
		return r.renderDecodeMapper(output, operation)
	case OpContainer:
		return r.renderDecodeOperations(output, operation.Operations)
	case OpArray:
		return r.renderDecodeArray(output, operation)
	case OpOption:
		return r.renderDecodeOption(output, operation)
	case OpSwitch:
		return r.renderDecodeSwitch(output, operation)
	case OpBitField:
		return r.renderDecodeBitField(output, operation)
	case OpBitFlags:
		return r.renderDecodeBitFlags(output, operation)
	case OpShared:
		return r.renderShared(output, operation, "Decode")
	case OpTerminatedLoop:
		return r.renderDecodeTerminatedLoop(output, operation)
	case OpVoid:
		return nil
	default:
		return fmt.Errorf("unsupported decode operation %q", operation.Kind)
	}
}

func (r *operationRenderer) renderEncodeOperation(output *sourceWriter, operation Operation) error {
	switch operation.Kind {
	case OpValue:
		return r.renderEncodeValue(output, operation)
	case OpMapper:
		return r.renderEncodeMapper(output, operation)
	case OpContainer:
		return r.renderEncodeOperations(output, operation.Operations)
	case OpArray:
		return r.renderEncodeArray(output, operation)
	case OpOption:
		return r.renderEncodeOption(output, operation)
	case OpSwitch:
		return r.renderEncodeSwitch(output, operation)
	case OpBitField:
		return r.renderEncodeBitField(output, operation)
	case OpBitFlags:
		return r.renderEncodeBitFlags(output, operation)
	case OpShared:
		return r.renderShared(output, operation, "Encode")
	case OpTerminatedLoop:
		return r.renderEncodeTerminatedLoop(output, operation)
	case OpVoid:
		return nil
	default:
		return fmt.Errorf("unsupported encode operation %q", operation.Kind)
	}
}

func (r *operationRenderer) renderDecodeOperations(output *sourceWriter, operations []Operation) error {
	for _, operation := range operations {
		if err := r.renderDecodeOperation(output, operation); err != nil {
			return generationPathError(operation.Path, err)
		}
	}
	return nil
}

func (r *operationRenderer) renderEncodeOperations(output *sourceWriter, operations []Operation) error {
	for _, operation := range operations {
		if err := r.renderEncodeOperation(output, operation); err != nil {
			return generationPathError(operation.Path, err)
		}
	}
	return nil
}

func (r *operationRenderer) renderDecodeValue(output *sourceWriter, operation Operation) error {
	if operation.Method == "" {
		return fmt.Errorf("value operation has no read method")
	}
	arguments := strconv.Quote(operation.Path)
	if operation.Count.Kind != CountNone {
		if operation.Count.Kind == CountType {
			if operation.Method == "ReadBuffer" {
				return fmt.Errorf("typed buffer count must use a combined buffer method")
			}
		} else {
			count, err := r.renderDecodeCount(output, operation)
			if err != nil {
				return err
			}
			arguments += ", " + count
		}
	}
	value := r.next("value")
	output.line("%s, err := buffer.%s(%s)", value, operation.Method, arguments)
	r.renderReturnError(output)
	output.line("%s = %s", operation.Value, value)
	return nil
}

func (r *operationRenderer) renderEncodeValue(output *sourceWriter, operation Operation) error {
	if operation.Method == "" {
		return fmt.Errorf("value operation has no write method")
	}
	if operation.Count.Kind != CountNone && operation.Count.Kind != CountType {
		count, err := r.renderEncodeCount(output, operation)
		if err != nil {
			return err
		}
		output.line("if len(%s) != %s {", operation.Value, count)
		output.indent++
		output.line(
			"return fmt.Errorf(%s, len(%s), %s)",
			strconv.Quote("field "+operation.Path+": buffer length %d does not match count %d"),
			operation.Value,
			count,
		)
		output.indent--
		output.line("}")
	}
	output.line("if err := buffer.%s(%s, %s); err != nil {", operation.Method, strconv.Quote(operation.Path), operation.Value)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderDecodeMapper(output *sourceWriter, operation Operation) error {
	mapper, ok := r.mapper(operation.Mapper)
	if !ok {
		return fmt.Errorf("mapper %q is not declared", operation.Mapper)
	}
	wire := r.next("value")
	output.line("%s, err := buffer.%s(%s)", wire, operation.Method, strconv.Quote(operation.Path))
	r.renderReturnError(output)
	mapped := r.next("mapped")
	output.line("%s, ok := %s[%s]", mapped, mapper.ReadTable, wire)
	output.line("if !ok {")
	output.indent++
	output.line(
		"return fmt.Errorf(%s, %s)",
		strconv.Quote("field "+operation.Path+": unknown mapper wire value %v"),
		wire,
	)
	output.indent--
	output.line("}")
	output.line("%s = %s", operation.Value, mapped)
	return nil
}

func (r *operationRenderer) renderEncodeMapper(output *sourceWriter, operation Operation) error {
	mapper, ok := r.mapper(operation.Mapper)
	if !ok {
		return fmt.Errorf("mapper %q is not declared", operation.Mapper)
	}
	wire := r.next("value")
	output.line("%s, ok := %s[%s]", wire, mapper.WriteTable, operation.Value)
	output.line("if !ok {")
	output.indent++
	output.line(
		"return fmt.Errorf(%s, %s)",
		strconv.Quote("field "+operation.Path+": unknown mapper symbol %q"),
		operation.Value,
	)
	output.indent--
	output.line("}")
	output.line("if err := buffer.%s(%s, %s); err != nil {", operation.Method, strconv.Quote(operation.Path), wire)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderDecodeArray(output *sourceWriter, operation Operation) error {
	count, err := r.renderDecodeCount(output, operation)
	if err != nil {
		return err
	}
	if operation.Count.Kind != CountFixed {
		if !strings.HasPrefix(operation.GoType, "[]") {
			return fmt.Errorf("non-fixed array has Go type %q", operation.GoType)
		}
		output.line("%s = make(%s, %s)", operation.Value, operation.GoType, count)
	}
	output.line("for %s := 0; %s < %s; %s++ {", operation.Index, operation.Index, count, operation.Index)
	output.indent++
	if err := r.renderDecodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("}")
	return nil
}

// renderDecodeTerminatedLoop emits a loop that grows until the sentinel byte
// appears.
//
// The element is appended after it decodes, not before, so a failed element
// cannot leave a half-filled entry in the slice. The collection limit is
// checked before each element rather than after, so a peer cannot make the
// decoder allocate past the bound and then be told about it.
func (r *operationRenderer) renderDecodeTerminatedLoop(output *sourceWriter, operation Operation) error {
	if !strings.HasPrefix(operation.GoType, "[]") {
		return fmt.Errorf("terminated loop has Go type %q", operation.GoType)
	}
	elementType := strings.TrimPrefix(operation.GoType, "[]")

	output.line("%s = make(%s, 0)", operation.Value, operation.GoType)
	output.line("for {")
	output.indent++
	done := r.next("done")
	output.line("%s, err := buffer.ReadTerminator(%s, %d)", done, strconv.Quote(operation.Path), operation.Terminator)
	r.renderReturnError(output)
	output.line("if %s {", done)
	output.indent++
	output.line("break")
	output.indent--
	output.line("}")
	output.line(
		"if err := buffer.ValidateCollection(%s, len(%s)+1); err != nil {",
		strconv.Quote(operation.Path),
		operation.Value,
	)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	// The element is appended zeroed and then decoded in place, so the element
	// operations address it the same way an array's do.
	element := r.next("element")
	output.line("var %s %s", element, elementType)
	output.line("%s = append(%s, %s)", operation.Value, operation.Value, element)
	output.line("%s := len(%s) - 1", operation.Index, operation.Value)
	if err := r.renderDecodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("}")

	return nil
}

// renderEncodeTerminatedLoop writes every element and then the sentinel.
func (r *operationRenderer) renderEncodeTerminatedLoop(output *sourceWriter, operation Operation) error {
	output.line("if err := buffer.ValidateCollection(%s, len(%s)); err != nil {", strconv.Quote(operation.Path), operation.Value)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	output.line("for %s := range %s {", operation.Index, operation.Value)
	output.indent++
	if err := r.renderEncodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("}")
	output.line("if err := buffer.WriteTerminator(%s, %d); err != nil {", strconv.Quote(operation.Path), operation.Terminator)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")

	return nil
}

func (r *operationRenderer) renderEncodeArray(output *sourceWriter, operation Operation) error {
	count, err := r.renderEncodeCount(output, operation)
	if err != nil {
		return err
	}
	if operation.Count.Kind != CountType {
		output.line("if len(%s) != %s {", operation.Value, count)
		output.indent++
		output.line(
			"return fmt.Errorf(%s, len(%s), %s)",
			strconv.Quote("field "+operation.Path+": collection length %d does not match count %d"),
			operation.Value,
			count,
		)
		output.indent--
		output.line("}")
	}
	output.line("for %s := range %s {", operation.Index, operation.Value)
	output.indent++
	if err := r.renderEncodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderDecodeOption(output *sourceWriter, operation Operation) error {
	if !strings.HasPrefix(operation.GoType, "*") {
		return fmt.Errorf("option has Go type %q", operation.GoType)
	}
	present := r.next("present")
	output.line("%s, err := buffer.%s(%s)", present, operation.Method, strconv.Quote(operation.Path))
	r.renderReturnError(output)
	output.line("if %s {", present)
	output.indent++
	output.line("%s = new(%s)", operation.Value, strings.TrimPrefix(operation.GoType, "*"))
	if err := r.renderDecodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("} else {")
	output.indent++
	output.line("%s = nil", operation.Value)
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderEncodeOption(output *sourceWriter, operation Operation) error {
	present := r.next("present")
	output.line("%s := %s != nil", present, operation.Value)
	output.line("if err := buffer.%s(%s, %s); err != nil {", operation.Method, strconv.Quote(operation.Path), present)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	output.line("if %s {", present)
	output.indent++
	if err := r.renderEncodeOperations(output, operation.Operations); err != nil {
		return err
	}
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderDecodeSwitch(output *sourceWriter, operation Operation) error {
	return r.renderSwitch(output, operation, true)
}

func (r *operationRenderer) renderEncodeSwitch(output *sourceWriter, operation Operation) error {
	return r.renderSwitch(output, operation, false)
}

func (r *operationRenderer) renderSwitch(output *sourceWriter, operation Operation, decode bool) error {
	seen := make(map[string]struct{}, len(operation.Cases))
	output.line("switch %s {", operation.Compare.Value)
	for _, item := range operation.Cases {
		if _, duplicate := seen[item.Match]; duplicate {
			return fmt.Errorf("duplicate switch match %s", item.Match)
		}
		seen[item.Match] = struct{}{}
		output.line("case %s:", item.Match)
		output.indent++
		var err error
		if decode {
			err = r.renderDecodeOperations(output, item.Operations)
		} else {
			err = r.renderEncodeOperations(output, item.Operations)
		}
		if err != nil {
			return err
		}
		output.indent--
	}
	if operation.HasDefault {
		output.line("default:")
		output.indent++
		var err error
		if decode {
			err = r.renderDecodeOperations(output, operation.Default.Operations)
		} else {
			err = r.renderEncodeOperations(output, operation.Default.Operations)
		}
		if err != nil {
			return err
		}
		output.indent--
	} else {
		output.line("default:")
		output.indent++
		output.line(
			"return fmt.Errorf(%s, %s)",
			strconv.Quote("field "+operation.Path+": unsupported switch value %v"),
			operation.Compare.Value,
		)
		output.indent--
	}
	output.line("}")
	return nil
}

func (r *operationRenderer) renderDecodeBitField(output *sourceWriter, operation Operation) error {
	declaration, ok := r.declarations[operation.Declaration]
	if !ok || declaration.Kind != DeclarationBitField {
		return fmt.Errorf("bitfield declaration %q is not declared", operation.Declaration)
	}
	packed := r.next("packed")
	output.line("%s, err := buffer.%s(%s)", packed, operation.Method, strconv.Quote(operation.Path))
	r.renderReturnError(output)
	for _, field := range declaration.BitField.Fields {
		segment := r.next("segment")
		output.line(
			"%s := (%s >> %d) & %s(%s)",
			segment,
			packed,
			field.Shift,
			operation.WireGoType,
			field.Mask,
		)
		output.line("%s.%s = %s(%s)", operation.Value, field.GoName, field.GoType, segment)
		if field.Signed && field.Size < goIntegerBits(field.GoType) {
			output.line("if %s&(%s(1)<<%d) != 0 {", segment, operation.WireGoType, field.Size-1)
			output.indent++
			output.line("%s.%s |= ^%s(%s)", operation.Value, field.GoName, field.GoType, field.Mask)
			output.indent--
			output.line("}")
		}
	}
	return nil
}

func (r *operationRenderer) renderEncodeBitField(output *sourceWriter, operation Operation) error {
	declaration, ok := r.declarations[operation.Declaration]
	if !ok || declaration.Kind != DeclarationBitField {
		return fmt.Errorf("bitfield declaration %q is not declared", operation.Declaration)
	}
	for _, field := range declaration.BitField.Fields {
		value := operation.Value + "." + field.GoName
		if field.Signed {
			if field.Size < goIntegerBits(field.GoType) {
				minimum := -(int64(1) << (field.Size - 1))
				maximum := (int64(1) << (field.Size - 1)) - 1
				output.line("if int64(%s) < %d || int64(%s) > %d {", value, minimum, value, maximum)
				output.indent++
				r.renderBitFieldRangeError(output, field, value)
				output.indent--
				output.line("}")
			}
		} else if field.Size < 64 {
			output.line("if uint64(%s) > uint64(%s) {", value, field.Mask)
			output.indent++
			r.renderBitFieldRangeError(output, field, value)
			output.indent--
			output.line("}")
		}
	}
	packed := r.next("packed")
	output.line("var %s %s", packed, operation.WireGoType)
	for _, field := range declaration.BitField.Fields {
		output.line(
			"%s |= (%s(%s.%s) & %s(%s)) << %d",
			packed,
			operation.WireGoType,
			operation.Value,
			field.GoName,
			operation.WireGoType,
			field.Mask,
			field.Shift,
		)
	}
	output.line("if err := buffer.%s(%s, %s); err != nil {", operation.Method, strconv.Quote(operation.Path), packed)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderBitFieldRangeError(output *sourceWriter, field BitFieldField, value string) {
	output.line(
		"return fmt.Errorf(%s, %s)",
		strconv.Quote("field "+field.Path+": bitfield value %v does not fit "+strconv.Itoa(field.Size)+" bits"),
		value,
	)
}

func (r *operationRenderer) renderDecodeBitFlags(output *sourceWriter, operation Operation) error {
	declaration, ok := r.declarations[operation.Declaration]
	if !ok || declaration.Kind != DeclarationBitFlags {
		return fmt.Errorf("bitflags declaration %q is not declared", operation.Declaration)
	}
	packed := r.next("packed")
	output.line("%s, err := buffer.%s(%s)", packed, operation.Method, strconv.Quote(operation.Path))
	r.renderReturnError(output)
	for _, field := range declaration.BitFlags.Fields {
		output.line("%s.%s = %s&%s(%s) != 0", operation.Value, field.GoName, packed, operation.WireGoType, field.Mask)
	}
	return nil
}

func (r *operationRenderer) renderEncodeBitFlags(output *sourceWriter, operation Operation) error {
	declaration, ok := r.declarations[operation.Declaration]
	if !ok || declaration.Kind != DeclarationBitFlags {
		return fmt.Errorf("bitflags declaration %q is not declared", operation.Declaration)
	}
	packed := r.next("packed")
	output.line("var %s %s", packed, operation.WireGoType)
	for _, field := range declaration.BitFlags.Fields {
		output.line("if %s.%s {", operation.Value, field.GoName)
		output.indent++
		output.line("%s |= %s(%s)", packed, operation.WireGoType, field.Mask)
		output.indent--
		output.line("}")
	}
	output.line("if err := buffer.%s(%s, %s); err != nil {", operation.Method, strconv.Quote(operation.Path), packed)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderDecodeCount(output *sourceWriter, operation Operation) (string, error) {
	count := operation.Count
	path := strconv.Quote(operation.Path)
	switch count.Kind {
	case CountNone:
		return "", fmt.Errorf("array or buffer has no count")
	case CountFixed:
		name := r.next("count")
		output.line("%s := %d", name, count.Fixed)
		if err := renderCollectionValidation(output, count.ValidateMethod, path, name); err != nil {
			return "", err
		}
		return name, nil
	case CountReference:
		name := r.next("count")
		output.line("%s := int(%s)", name, count.Reference.Value)
		output.line("if %s < 0 || %s(%s) != %s {", name, count.Reference.GoType, name, count.Reference.Value)
		output.indent++
		output.line(
			"return fmt.Errorf(%s, %s)",
			strconv.Quote("field "+operation.Path+": collection count %v cannot be represented as int"),
			count.Reference.Value,
		)
		output.indent--
		output.line("}")
		if err := renderCollectionValidation(output, count.ValidateMethod, path, name); err != nil {
			return "", err
		}
		return name, nil
	case CountType:
		if operation.Method == "ReadCollectionLength" {
			name := r.next("count")
			output.line("%s, err := buffer.%s(%s)", name, operation.Method, path)
			r.renderReturnError(output)
			return name, nil
		}
		wire := r.next("wireCount")
		output.line("%s, err := buffer.%s(%s)", wire, operation.Method, path)
		r.renderReturnError(output)
		name := r.next("count")
		output.line("%s := int(%s)", name, wire)
		output.line("if %s < 0 || %s(%s) != %s {", name, count.WireGoType, name, wire)
		output.indent++
		output.line(
			"return fmt.Errorf(%s, %s)",
			strconv.Quote("field "+operation.Path+": collection count %v cannot be represented as int"),
			wire,
		)
		output.indent--
		output.line("}")
		if err := renderCollectionValidation(output, count.ValidateMethod, path, name); err != nil {
			return "", err
		}
		return name, nil
	default:
		return "", fmt.Errorf("array or buffer has unsupported count kind %q", count.Kind)
	}
}

func (r *operationRenderer) renderEncodeCount(output *sourceWriter, operation Operation) (string, error) {
	count := operation.Count
	path := strconv.Quote(operation.Path)
	switch count.Kind {
	case CountNone:
		return "", fmt.Errorf("array or buffer has no count")
	case CountFixed:
		name := r.next("count")
		output.line("%s := %d", name, count.Fixed)
		if err := renderCollectionValidation(output, count.ValidateMethod, path, name); err != nil {
			return "", err
		}
		return name, nil
	case CountReference:
		name := r.next("count")
		output.line("%s := int(%s)", name, count.Reference.Value)
		output.line("if %s < 0 || %s(%s) != %s {", name, count.Reference.GoType, name, count.Reference.Value)
		output.indent++
		output.line(
			"return fmt.Errorf(%s, %s)",
			strconv.Quote("field "+operation.Path+": collection count %v cannot be represented as int"),
			count.Reference.Value,
		)
		output.indent--
		output.line("}")
		if err := renderCollectionValidation(output, count.ValidateMethod, path, name); err != nil {
			return "", err
		}
		return name, nil
	case CountType:
		name := r.next("count")
		output.line("%s := len(%s)", name, operation.Value)
		if operation.Method == "WriteCollectionLength" {
			output.line("if err := buffer.%s(%s, %s); err != nil {", operation.Method, path, name)
			output.indent++
			output.line("return err")
			output.indent--
			output.line("}")
			return name, nil
		}
		if err := renderCollectionValidation(output, count.ValidateMethod, path, name); err != nil {
			return "", err
		}
		maximum, ok := maximumIntegerValue(count.WireGoType)
		if !ok {
			return "", fmt.Errorf("unsupported count wire type %q", count.WireGoType)
		}
		output.line("if uint64(%s) > uint64(%s) {", name, maximum)
		output.indent++
		output.line(
			"return fmt.Errorf(%s, %s)",
			strconv.Quote("field "+operation.Path+": collection count %d does not fit "+count.WireGoType),
			name,
		)
		output.indent--
		output.line("}")
		output.line("if err := buffer.%s(%s, %s(%s)); err != nil {", operation.Method, path, count.WireGoType, name)
		output.indent++
		output.line("return err")
		output.indent--
		output.line("}")
		return name, nil
	default:
		return "", fmt.Errorf("array or buffer has unsupported count kind %q", count.Kind)
	}
}

func renderCollectionValidation(output *sourceWriter, method, path, count string) error {
	if method == "" {
		return fmt.Errorf("collection count has no validation method")
	}
	output.line("if err := buffer.%s(%s, %s); err != nil {", method, path, count)
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
	return nil
}

func (r *operationRenderer) renderReturnError(output *sourceWriter) {
	output.line("if err != nil {")
	output.indent++
	output.line("return err")
	output.indent--
	output.line("}")
}

func (r *operationRenderer) next(prefix string) string {
	r.temporary++
	return prefix + strconv.Itoa(r.temporary)
}

func (r *operationRenderer) mapper(name string) (Mapper, bool) {
	mapper, ok := r.mappers[name]
	return mapper, ok
}

func renderDescriptor(model *Model) string {
	var output sourceWriter
	output.line("type packetCodec interface {")
	output.indent++
	output.line("java.PacketValue")
	output.line("Decode(*java.Buffer) error")
	output.line("Encode(*java.Buffer) error")
	output.indent--
	output.line("}")
	output.line("")
	output.line("type packetKey struct {")
	output.indent++
	output.line("State protocol.State")
	output.line("Direction protocol.Direction")
	output.line("ID int32")
	output.indent--
	output.line("}")
	output.line("")
	output.line("type packetFactory func() packetCodec")
	output.line("")
	output.line("var packetFactories = map[packetKey]packetFactory{")
	output.indent++
	for _, factory := range model.Factories {
		stateValue := factory.StateValue
		if stateValue == "" {
			stateValue = strconv.Quote(factory.State)
		}
		output.line(
			"{State: protocol.State(%s), Direction: %s, ID: %s}: func() packetCodec { return new(%s) },",
			stateValue,
			factory.DirectionValue,
			packetIDLiteral(factory.SourceID, factory.ID),
			factory.PacketType,
		)
	}
	output.indent--
	output.line("}")
	output.line("")
	output.line("func newPacket(state protocol.State, direction protocol.Direction, id int32) (packetCodec, bool) {")
	output.indent++
	output.line("factory, ok := packetFactories[packetKey{State: state, Direction: direction, ID: id}]")
	output.line("if !ok {")
	output.indent++
	output.line("return nil, false")
	output.indent--
	output.line("}")
	output.line("return factory(), true")
	output.indent--
	output.line("}")
	return strings.TrimSpace(output.String()) + "\n"
}

func modelPacketCount(model *Model) int {
	count := 0
	for _, state := range model.States {
		for _, direction := range state.Directions {
			count += len(direction.Packets)
		}
	}
	return count
}

func packetIDLiteral(source string, id int32) string {
	if source != "" {
		return source
	}
	return strconv.FormatInt(int64(id), 10)
}

func maximumIntegerValue(goType string) (string, bool) {
	switch goType {
	case "int8":
		return "127", true
	case "uint8":
		return "255", true
	case "int16":
		return "32767", true
	case "uint16":
		return "65535", true
	case "int32":
		return "2147483647", true
	case "uint32":
		return "4294967295", true
	case "int64":
		return "9223372036854775807", true
	case "uint64":
		return "18446744073709551615", true
	default:
		return "", false
	}
}

func goIntegerBits(goType string) int {
	for _, prefix := range []string{"uint", "int"} {
		if strings.HasPrefix(goType, prefix) {
			bits, _ := strconv.Atoi(strings.TrimPrefix(goType, prefix))
			return bits
		}
	}
	return 0
}

func generationPathError(path string, err error) error {
	if err == nil {
		return nil
	}
	if path == "" {
		return fmt.Errorf("packetgen: %w", err)
	}
	return fmt.Errorf("packetgen: %s: %w", path, err)
}
