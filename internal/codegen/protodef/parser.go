package protodef

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

var (
	stateOrder     = []string{"handshaking", "status", "login", "play"}
	directionOrder = []string{"toClient", "toServer"}
)

// Parse decodes and validates a complete ProtoDef document.
func Parse(raw []byte) (*Schema, error) {
	root, err := decodeObject(raw, "$")
	if err != nil {
		return nil, err
	}

	typesRaw, ok := root["types"]
	if !ok {
		return nil, pathError("$.types", "required")
	}
	nativeNames, err := collectNativeNames(typesRaw, "$.types")
	if err != nil {
		return nil, err
	}
	types, typeNames, err := parseDefinitions(typesRaw, "$.types", nativeNames)
	if err != nil {
		return nil, err
	}
	if err := resolveDefinitions(types, nil); err != nil {
		return nil, err
	}
	if err := validateAliasCycles(types, nil); err != nil {
		return nil, err
	}

	schema := &Schema{Types: types, TypeNames: typeNames}
	for _, stateName := range orderedPresentKeys(root, stateOrder, "types") {
		stateRaw := root[stateName]
		stateObject, decodeErr := decodeObject(stateRaw, jsonPath("$", stateName))
		if decodeErr != nil {
			return nil, decodeErr
		}

		state := State{Name: stateName}
		for _, directionName := range orderedPresentKeys(stateObject, directionOrder) {
			direction, parseErr := parseDirection(
				stateObject[directionName],
				jsonPath(jsonPath("$", stateName), directionName),
				directionName,
				types,
				nativeNames,
			)
			if parseErr != nil {
				return nil, parseErr
			}
			state.Directions = append(state.Directions, direction)
		}
		schema.States = append(schema.States, state)
	}

	return schema, nil
}

func parseDirection(
	raw json.RawMessage,
	path string,
	name string,
	global map[string]*TypeNode,
	nativeNames map[string]struct{},
) (Direction, error) {
	object, err := decodeObject(raw, path)
	if err != nil {
		return Direction{}, err
	}
	typesRaw, ok := object["types"]
	if !ok {
		return Direction{}, pathError(jsonPath(path, "types"), "required")
	}
	types, typeNames, err := parseDefinitions(typesRaw, jsonPath(path, "types"), nativeNames)
	if err != nil {
		return Direction{}, err
	}
	if err := resolveDefinitions(types, global); err != nil {
		return Direction{}, err
	}
	if err := validateAliasCycles(types, global); err != nil {
		return Direction{}, err
	}

	direction := Direction{Name: name, Types: types, TypeNames: typeNames}
	packet, ok := types["packet"]
	if !ok {
		return Direction{}, pathError(jsonPath(jsonPath(path, "types"), "packet"), "required")
	}
	packetPath := jsonPath(jsonPath(path, "types"), "packet")
	if packet.Kind != KindContainer || len(packet.Fields) < 2 {
		return Direction{}, pathError(packetPath, "packet must be a container with name and params fields")
	}
	direction.PacketMap = packet.Fields[0].Type
	direction.PacketSwitch = packet.Fields[1].Type
	if direction.PacketMap.Kind != KindMapper {
		return Direction{}, pathError(packet.Fields[0].Type.path, "packet name field must be a mapper")
	}
	if direction.PacketSwitch.Kind != KindSwitch {
		return Direction{}, pathError(packet.Fields[1].Type.path, "packet params field must be a switch")
	}

	direction.Packets, err = buildPacketInventory(direction.PacketMap, direction.PacketSwitch)
	if err != nil {
		return Direction{}, err
	}
	return direction, nil
}

func buildPacketInventory(mapper, switchNode *TypeNode) ([]Packet, error) {
	cases := make(map[string]*TypeNode, len(switchNode.Cases))
	for _, item := range switchNode.Cases {
		cases[item.Key] = item.Type
	}

	seenIDs := make(map[int]string, len(mapper.Mappings))
	seenNames := make(map[string]string, len(mapper.Mappings))
	packets := make([]Packet, 0, len(mapper.Mappings))
	for _, mapping := range mapper.Mappings {
		id, err := parsePacketID(mapping.Key)
		if err != nil {
			return nil, pathError(mapping.path, "invalid packet ID %q", mapping.Key)
		}
		if _, ok := seenIDs[id]; ok {
			return nil, pathError(mapping.path, "duplicate packet ID %d", id)
		}
		seenIDs[id] = mapping.Key
		if _, ok := seenNames[mapping.Value]; ok {
			return nil, pathError(mapping.path, "duplicate packet name %q", mapping.Value)
		}
		seenNames[mapping.Value] = mapping.Key

		typeNode, ok := cases[mapping.Value]
		if !ok {
			return nil, pathError(switchNode.path+"[1].fields", "missing packet type for %q", mapping.Value)
		}
		packets = append(packets, Packet{
			ID:       id,
			IDKey:    mapping.Key,
			Name:     mapping.Value,
			TypeName: typeNode.Name,
			Type:     typeNode,
		})
	}

	return packets, nil
}

func parsePacketID(value string) (int, error) {
	base := 10
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "-0x") {
		base = 0
	}
	id, err := strconv.ParseInt(value, base, 32)
	return int(id), err
}

func collectNativeNames(raw json.RawMessage, path string) (map[string]struct{}, error) {
	object, err := decodeObject(raw, path)
	if err != nil {
		return nil, err
	}
	names := make(map[string]struct{})
	for name, definition := range object {
		var marker string
		if json.Unmarshal(definition, &marker) == nil && marker == "native" {
			names[name] = struct{}{}
		}
	}
	return names, nil
}

func parseDefinitions(
	raw json.RawMessage,
	path string,
	nativeNames map[string]struct{},
) (map[string]*TypeNode, []string, error) {
	object, err := decodeObject(raw, path)
	if err != nil {
		return nil, nil, err
	}
	names := sortedKeys(object)
	definitions := make(map[string]*TypeNode, len(object))
	for _, name := range names {
		definitionPath := jsonPath(path, name)
		var marker string
		if json.Unmarshal(object[name], &marker) == nil && marker == "native" {
			definitions[name] = &TypeNode{Kind: KindNative, Name: name, path: definitionPath, definition: true}
			continue
		}
		node, parseErr := parseTypeNode(object[name], definitionPath, nativeNames, nil)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		definitions[name] = node
	}
	return definitions, names, nil
}

func parseTypeNode(
	raw json.RawMessage,
	path string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*TypeNode, error) {
	var name string
	if err := json.Unmarshal(raw, &name); err == nil {
		if name == "" {
			return nil, pathError(path, "type name must not be empty")
		}
		kind := KindAlias
		if _, ok := nativeNames[name]; ok {
			kind = KindPrimitive
		}
		return &TypeNode{Kind: kind, Name: name, path: path}, nil
	}

	var tuple []json.RawMessage
	if err := json.Unmarshal(raw, &tuple); err != nil || len(tuple) != 2 {
		return nil, pathError(path, "type node must be a string or a two-element array")
	}
	if err := json.Unmarshal(tuple[0], &name); err != nil || name == "" {
		return nil, pathError(path+"[0]", "operator must be a non-empty string")
	}
	parameterPath := path + "[1]"

	switch name {
	case "container":
		return parseContainer(tuple[1], parameterPath, nativeNames, scopes)
	case "array":
		return parseArray(tuple[1], parameterPath, nativeNames, scopes)
	case "switch":
		return parseSwitch(tuple[1], parameterPath, nativeNames, scopes)
	case "option":
		element, err := parseTypeNode(tuple[1], parameterPath, nativeNames, scopes)
		if err != nil {
			return nil, err
		}
		return &TypeNode{Kind: KindOption, Element: element, path: path}, nil
	case "buffer":
		count, err := parseCount(tuple[1], parameterPath, nativeNames, scopes)
		if err != nil {
			return nil, err
		}
		return &TypeNode{Kind: KindBuffer, Count: count, path: path}, nil
	case "mapper":
		return parseMapper(tuple[1], parameterPath, nativeNames, scopes)
	case "bitfield":
		return parseBitField(tuple[1], parameterPath, path)
	case "bitflags":
		return parseBitFlags(tuple[1], parameterPath, path, nativeNames, scopes)
	default:
		kind := KindAlias
		if _, ok := nativeNames[name]; ok {
			kind = KindNative
		}
		node := &TypeNode{Kind: kind, Name: name, path: path}
		count, hasCount, err := parseOptionalCount(tuple[1], parameterPath, nativeNames, scopes)
		if err != nil {
			return nil, err
		}
		if hasCount {
			node.Count = count
		}
		node.Arguments, err = parseArguments(tuple[1], parameterPath, nativeNames, scopes, hasCount)
		if err != nil {
			return nil, err
		}
		return node, nil
	}
}

func parseContainer(
	raw json.RawMessage,
	parameterPath string,
	nativeNames map[string]struct{},
	parentScopes []map[string]struct{},
) (*TypeNode, error) {
	var rawFields []json.RawMessage
	if err := json.Unmarshal(raw, &rawFields); err != nil {
		return nil, pathError(parameterPath, "container fields must be an array")
	}
	fields := make([]Field, 0, len(rawFields))
	local := make(map[string]struct{})
	scopes := append(append([]map[string]struct{}{}, parentScopes...), local)
	for i, rawField := range rawFields {
		fieldPath := fmt.Sprintf("%s[%d]", parameterPath, i)
		object, err := decodeObject(rawField, fieldPath)
		if err != nil {
			return nil, err
		}
		var field Field
		if nameRaw, ok := object["name"]; ok {
			if err := json.Unmarshal(nameRaw, &field.Name); err != nil || field.Name == "" {
				return nil, pathError(jsonPath(fieldPath, "name"), "field name must be a non-empty string")
			}
		}
		if anonymousRaw, ok := object["anon"]; ok {
			if err := json.Unmarshal(anonymousRaw, &field.Anonymous); err != nil {
				return nil, pathError(jsonPath(fieldPath, "anon"), "must be a boolean")
			}
		}
		if field.Name == "" && !field.Anonymous {
			return nil, pathError(fieldPath, "field requires name or anon=true")
		}
		if field.Name != "" {
			if _, duplicate := local[field.Name]; duplicate {
				return nil, pathError(jsonPath(fieldPath, "name"), "duplicate field %q", field.Name)
			}
		}
		typeRaw, ok := object["type"]
		if !ok {
			return nil, pathError(jsonPath(fieldPath, "type"), "required")
		}
		field.Type, err = parseTypeNode(typeRaw, jsonPath(fieldPath, "type"), nativeNames, scopes)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
		if field.Name != "" {
			local[field.Name] = struct{}{}
		} else if field.Anonymous {
			for _, name := range visibleFieldNames(field.Type) {
				local[name] = struct{}{}
			}
		}
	}
	return &TypeNode{Kind: KindContainer, Fields: fields, path: strings.TrimSuffix(parameterPath, "[1]")}, nil
}

func visibleFieldNames(node *TypeNode) []string {
	switch node.Kind {
	case KindContainer:
		names := make([]string, 0, len(node.Fields))
		for _, field := range node.Fields {
			if field.Name != "" {
				names = append(names, field.Name)
			} else if field.Anonymous {
				names = append(names, visibleFieldNames(field.Type)...)
			}
		}
		return names
	case KindBitField:
		names := make([]string, len(node.Bits))
		for i, bit := range node.Bits {
			names[i] = bit.Name
		}
		return names
	case KindAlias,
		KindPrimitive,
		KindNative,
		KindArray,
		KindSwitch,
		KindOption,
		KindBuffer,
		KindMapper,
		KindBitFlags:
		return nil
	}
	return nil
}

func parseArray(
	raw json.RawMessage,
	parameterPath string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*TypeNode, error) {
	object, err := decodeObject(raw, parameterPath)
	if err != nil {
		return nil, err
	}
	elementRaw, ok := object["type"]
	if !ok {
		return nil, pathError(jsonPath(parameterPath, "type"), "required")
	}
	element, err := parseTypeNode(elementRaw, jsonPath(parameterPath, "type"), nativeNames, scopes)
	if err != nil {
		return nil, err
	}
	count, err := parseCountObject(object, parameterPath, nativeNames, scopes)
	if err != nil {
		return nil, err
	}
	return &TypeNode{Kind: KindArray, Element: element, Count: count, path: strings.TrimSuffix(parameterPath, "[1]")}, nil
}

func parseSwitch(
	raw json.RawMessage,
	parameterPath string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*TypeNode, error) {
	object, err := decodeObject(raw, parameterPath)
	if err != nil {
		return nil, err
	}
	var compareTo string
	compareRaw, ok := object["compareTo"]
	if !ok {
		return nil, pathError(jsonPath(parameterPath, "compareTo"), "required")
	}
	if err := json.Unmarshal(compareRaw, &compareTo); err != nil || compareTo == "" {
		return nil, pathError(jsonPath(parameterPath, "compareTo"), "must be a non-empty string")
	}
	fieldsRaw, ok := object["fields"]
	if !ok {
		return nil, pathError(jsonPath(parameterPath, "fields"), "required")
	}
	fields, err := decodeObject(fieldsRaw, jsonPath(parameterPath, "fields"))
	if err != nil {
		return nil, err
	}
	if !fieldReferenceExists(compareTo, scopes) {
		return nil, pathError(jsonPath(parameterPath, "compareTo"), "unknown field reference %q", compareTo)
	}
	node := &TypeNode{Kind: KindSwitch, CompareTo: compareTo, path: strings.TrimSuffix(parameterPath, "[1]")}
	for _, key := range sortedKeys(fields) {
		caseType, parseErr := parseTypeNode(fields[key], jsonMapPath(jsonPath(parameterPath, "fields"), key), nativeNames, scopes)
		if parseErr != nil {
			return nil, parseErr
		}
		node.Cases = append(node.Cases, SwitchCase{Key: key, Type: caseType})
	}
	if defaultRaw, ok := object["default"]; ok {
		node.Default, err = parseTypeNode(defaultRaw, jsonPath(parameterPath, "default"), nativeNames, scopes)
		if err != nil {
			return nil, err
		}
	}
	return node, nil
}

func parseMapper(
	raw json.RawMessage,
	parameterPath string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*TypeNode, error) {
	object, err := decodeObject(raw, parameterPath)
	if err != nil {
		return nil, err
	}
	typeRaw, ok := object["type"]
	if !ok {
		return nil, pathError(jsonPath(parameterPath, "type"), "required")
	}
	element, err := parseTypeNode(typeRaw, jsonPath(parameterPath, "type"), nativeNames, scopes)
	if err != nil {
		return nil, err
	}
	mappingsRaw, ok := object["mappings"]
	if !ok {
		return nil, pathError(jsonPath(parameterPath, "mappings"), "required")
	}
	mappingsPath := jsonPath(parameterPath, "mappings")
	mappingEntries, err := decodeObjectEntries(mappingsRaw, mappingsPath, false)
	if err != nil {
		return nil, err
	}
	node := &TypeNode{Kind: KindMapper, Element: element, path: strings.TrimSuffix(parameterPath, "[1]")}
	for _, entry := range mappingEntries {
		var value string
		mappingPath := jsonMapPath(mappingsPath, entry.Key)
		if err := json.Unmarshal(entry.Value, &value); err != nil {
			return nil, pathError(mappingPath, "mapping value must be a string")
		}
		node.Mappings = append(node.Mappings, Mapping{Key: entry.Key, Value: value, path: mappingPath})
	}
	return node, nil
}

func parseBitField(raw json.RawMessage, parameterPath, nodePath string) (*TypeNode, error) {
	var rawBits []json.RawMessage
	if err := json.Unmarshal(raw, &rawBits); err != nil {
		return nil, pathError(parameterPath, "bitfield entries must be an array")
	}
	node := &TypeNode{Kind: KindBitField, path: nodePath}
	seen := make(map[string]struct{}, len(rawBits))
	for i, rawBit := range rawBits {
		bitPath := fmt.Sprintf("%s[%d]", parameterPath, i)
		object, err := decodeObject(rawBit, bitPath)
		if err != nil {
			return nil, err
		}
		var bit BitField
		if err := requiredString(object, "name", bitPath, &bit.Name); err != nil {
			return nil, err
		}
		if _, duplicate := seen[bit.Name]; duplicate {
			return nil, pathError(jsonPath(bitPath, "name"), "duplicate field %q", bit.Name)
		}
		seen[bit.Name] = struct{}{}
		if err := requiredInt(object, "size", bitPath, &bit.Size); err != nil {
			return nil, err
		}
		if bit.Size <= 0 {
			return nil, pathError(jsonPath(bitPath, "size"), "must be positive")
		}
		signedRaw, ok := object["signed"]
		if !ok {
			return nil, pathError(jsonPath(bitPath, "signed"), "required")
		}
		if err := json.Unmarshal(signedRaw, &bit.Signed); err != nil {
			return nil, pathError(jsonPath(bitPath, "signed"), "must be a boolean")
		}
		node.Bits = append(node.Bits, bit)
	}
	return node, nil
}

func parseBitFlags(
	raw json.RawMessage,
	parameterPath string,
	nodePath string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*TypeNode, error) {
	object, err := decodeObject(raw, parameterPath)
	if err != nil {
		return nil, err
	}
	typeRaw, ok := object["type"]
	if !ok {
		return nil, pathError(jsonPath(parameterPath, "type"), "required")
	}
	element, err := parseTypeNode(typeRaw, jsonPath(parameterPath, "type"), nativeNames, scopes)
	if err != nil {
		return nil, err
	}
	flagsRaw, ok := object["flags"]
	if !ok {
		return nil, pathError(jsonPath(parameterPath, "flags"), "required")
	}
	var flags []string
	if err := json.Unmarshal(flagsRaw, &flags); err != nil {
		return nil, pathError(jsonPath(parameterPath, "flags"), "flags must be an array of strings")
	}
	seen := make(map[string]struct{}, len(flags))
	for i, flag := range flags {
		if flag == "" {
			return nil, pathError(fmt.Sprintf("%s[%d]", jsonPath(parameterPath, "flags"), i), "flag name must not be empty")
		}
		if _, duplicate := seen[flag]; duplicate {
			return nil, pathError(fmt.Sprintf("%s[%d]", jsonPath(parameterPath, "flags"), i), "duplicate flag %q", flag)
		}
		seen[flag] = struct{}{}
	}
	return &TypeNode{Kind: KindBitFlags, Element: element, Flags: flags, path: nodePath}, nil
}

func parseCount(
	raw json.RawMessage,
	path string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*Count, error) {
	object, err := decodeObject(raw, path)
	if err != nil {
		return nil, err
	}
	return parseCountObject(object, path, nativeNames, scopes)
}

func parseOptionalCount(
	raw json.RawMessage,
	path string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*Count, bool, error) {
	object, err := decodeObject(raw, path)
	if err != nil {
		return nil, false, err
	}
	_, hasCount := object["count"]
	_, hasCountType := object["countType"]
	if !hasCount && !hasCountType {
		return nil, false, nil
	}
	count, err := parseCountObject(object, path, nativeNames, scopes)
	return count, true, err
}

func parseCountObject(
	object map[string]json.RawMessage,
	path string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
) (*Count, error) {
	countRaw, hasCount := object["count"]
	countTypeRaw, hasCountType := object["countType"]
	if hasCount == hasCountType {
		return nil, pathError(path, "exactly one of count or countType is required")
	}
	if hasCountType {
		var typeName string
		if err := json.Unmarshal(countTypeRaw, &typeName); err != nil || typeName == "" {
			return nil, pathError(jsonPath(path, "countType"), "must be a non-empty type name")
		}
		typeNode, err := parseTypeNode(countTypeRaw, jsonPath(path, "countType"), nativeNames, scopes)
		if err != nil {
			return nil, err
		}
		return &Count{Kind: CountType, Type: typeNode}, nil
	}

	var fixed int
	if err := json.Unmarshal(countRaw, &fixed); err == nil {
		if fixed < 0 {
			return nil, pathError(jsonPath(path, "count"), "fixed count must not be negative")
		}
		return &Count{Kind: CountFixed, Fixed: fixed}, nil
	}
	var reference string
	if err := json.Unmarshal(countRaw, &reference); err != nil || reference == "" {
		return nil, pathError(jsonPath(path, "count"), "must be a non-negative integer or field reference")
	}
	if !fieldReferenceExists(reference, scopes) {
		return nil, pathError(jsonPath(path, "count"), "unknown field reference %q", reference)
	}
	return &Count{Kind: CountReference, Reference: reference}, nil
}

// fieldReferenceExists reports whether a reference names a field that is in
// scope.
//
// A reference written with "../" names an exact level and is resolved there
// and nowhere else. A bare name resolves lexically: the innermost scope first,
// then outward. Protocol 775 needs the outward walk. DebugSubscriptionUpdate
// discriminates a nested switch on "type", a field of the container two scopes
// out, and there is no other "type" anywhere in the chain — resolving it in the
// innermost scope alone leaves the packet uncompilable.
func fieldReferenceExists(reference string, scopes []map[string]struct{}) bool {
	if strings.HasPrefix(reference, "$") {
		return true
	}

	level := len(scopes) - 1
	explicit := false
	for strings.HasPrefix(reference, "../") {
		level--
		explicit = true
		reference = strings.TrimPrefix(reference, "../")
	}
	if level < 0 || reference == "" {
		return false
	}
	if strings.Contains(reference, "/") {
		reference = strings.Split(reference, "/")[0]
	}

	if explicit {
		_, ok := scopes[level][reference]

		return ok
	}
	for ; level >= 0; level-- {
		if _, ok := scopes[level][reference]; ok {
			return true
		}
	}

	return false
}

func parseArguments(
	raw json.RawMessage,
	path string,
	nativeNames map[string]struct{},
	scopes []map[string]struct{},
	skipCount bool,
) ([]Argument, error) {
	object, err := decodeObject(raw, path)
	if err != nil {
		return nil, err
	}
	arguments := make([]Argument, 0, len(object))
	for _, name := range sortedKeys(object) {
		if skipCount && (name == "count" || name == "countType") {
			continue
		}
		argument := Argument{Name: name}
		argumentPath := jsonPath(path, name)
		if name == "type" {
			argument.Type, err = parseTypeNode(object[name], argumentPath, nativeNames, scopes)
			if err != nil {
				return nil, err
			}
			arguments = append(arguments, argument)
			continue
		}
		if name == "compareTo" {
			if err := json.Unmarshal(object[name], &argument.String); err != nil || argument.String == "" {
				return nil, pathError(argumentPath, "must be a non-empty string")
			}
			if !fieldReferenceExists(argument.String, scopes) {
				return nil, pathError(argumentPath, "unknown field reference %q", argument.String)
			}
			arguments = append(arguments, argument)
			continue
		}
		if json.Unmarshal(object[name], &argument.String) == nil {
			arguments = append(arguments, argument)
			continue
		}
		var number json.Number
		decoder := json.NewDecoder(bytes.NewReader(object[name]))
		decoder.UseNumber()
		if decoder.Decode(&number) == nil {
			argument.Number = number.String()
			arguments = append(arguments, argument)
			continue
		}
		var boolean bool
		if json.Unmarshal(object[name], &boolean) == nil {
			argument.Bool = &boolean
			arguments = append(arguments, argument)
			continue
		}
		argument.Raw = append([]byte(nil), object[name]...)
		arguments = append(arguments, argument)
	}
	return arguments, nil
}

func resolveDefinitions(definitions, global map[string]*TypeNode) error {
	for _, name := range sortedNodeKeys(definitions) {
		if err := resolveNode(definitions[name], definitions, global); err != nil {
			return err
		}
	}
	return nil
}

func resolveNode(node *TypeNode, local, global map[string]*TypeNode) error {
	if node == nil {
		return nil
	}
	if node.Kind == KindAlias || node.Kind == KindPrimitive || node.Kind == KindNative && !node.definition {
		target, ok := local[node.Name]
		if !ok && global != nil {
			target, ok = global[node.Name]
		}
		if !ok {
			return pathError(node.path, "unresolved named type %q", node.Name)
		}
		node.Target = target
	}
	for i := range node.Arguments {
		if err := resolveNode(node.Arguments[i].Type, local, global); err != nil {
			return err
		}
	}
	for i := range node.Fields {
		if err := resolveNode(node.Fields[i].Type, local, global); err != nil {
			return err
		}
	}
	if err := resolveNode(node.Element, local, global); err != nil {
		return err
	}
	if node.Count != nil {
		if err := resolveNode(node.Count.Type, local, global); err != nil {
			return err
		}
	}
	for i := range node.Cases {
		if err := resolveNode(node.Cases[i].Type, local, global); err != nil {
			return err
		}
	}
	return resolveNode(node.Default, local, global)
}

func validateAliasCycles(definitions, global map[string]*TypeNode) error {
	for _, name := range sortedNodeKeys(definitions) {
		chain := []string{name}
		seen := map[*TypeNode]int{definitions[name]: 0}
		node := definitions[name]
		for node.Kind == KindAlias {
			target := node.Target
			if target == nil {
				break
			}
			targetName := definitionName(target, definitions, global)
			if index, ok := seen[target]; ok {
				cycle := append(append([]string{}, chain[index:]...), targetName)
				return pathError(definitions[name].path, "alias cycle: %s", strings.Join(cycle, " -> "))
			}
			seen[target] = len(chain)
			chain = append(chain, targetName)
			node = target
		}
	}
	return nil
}

func definitionName(target *TypeNode, local, global map[string]*TypeNode) string {
	for _, name := range sortedNodeKeys(local) {
		if local[name] == target {
			return name
		}
	}
	for _, name := range sortedNodeKeys(global) {
		if global[name] == target {
			return name
		}
	}
	return target.Name
}

type objectEntry struct {
	Key   string
	Value json.RawMessage
}

func decodeObject(raw []byte, path string) (map[string]json.RawMessage, error) {
	entries, err := decodeObjectEntries(raw, path, true)
	if err != nil {
		return nil, err
	}
	object := make(map[string]json.RawMessage, len(entries))
	for _, entry := range entries {
		object[entry.Key] = entry.Value
	}
	return object, nil
}

func decodeObjectEntries(raw []byte, path string, rejectDuplicates bool) ([]objectEntry, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return nil, pathError(path, "invalid object: %v", err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, pathError(path, "invalid object: must be an object")
	}

	entries := make([]objectEntry, 0)
	seen := make(map[string]struct{})
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return nil, pathError(path, "invalid object: %v", tokenErr)
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, pathError(path, "invalid object: object key must be a string")
		}
		var value json.RawMessage
		if decodeErr := decoder.Decode(&value); decodeErr != nil {
			return nil, pathError(jsonMapPath(path, key), "invalid value: %v", decodeErr)
		}
		if _, duplicate := seen[key]; duplicate && rejectDuplicates {
			return nil, pathError(jsonMapPath(path, key), "duplicate object key %q", key)
		}
		seen[key] = struct{}{}
		entries = append(entries, objectEntry{Key: key, Value: value})
	}
	if _, err := decoder.Token(); err != nil {
		return nil, pathError(path, "invalid object: %v", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			err = fmt.Errorf("unexpected trailing value")
		}
		return nil, pathError(path, "invalid object: %v", err)
	}
	return entries, nil
}

func requiredString(object map[string]json.RawMessage, name, path string, target *string) error {
	raw, ok := object[name]
	if !ok {
		return pathError(jsonPath(path, name), "required")
	}
	if err := json.Unmarshal(raw, target); err != nil || *target == "" {
		return pathError(jsonPath(path, name), "must be a non-empty string")
	}
	return nil
}

func requiredInt(object map[string]json.RawMessage, name, path string, target *int) error {
	raw, ok := object[name]
	if !ok {
		return pathError(jsonPath(path, name), "required")
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return pathError(jsonPath(path, name), "must be an integer")
	}
	return nil
}

func orderedPresentKeys(object map[string]json.RawMessage, preferred []string, excluded ...string) []string {
	exclude := make(map[string]struct{}, len(excluded))
	for _, name := range excluded {
		exclude[name] = struct{}{}
	}
	seen := make(map[string]struct{}, len(preferred))
	result := make([]string, 0, len(object))
	for _, name := range preferred {
		if _, ok := object[name]; ok {
			result = append(result, name)
			seen[name] = struct{}{}
		}
	}
	var rest []string
	for name := range object {
		if _, ok := exclude[name]; ok {
			continue
		}
		if _, ok := seen[name]; !ok {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	return append(result, rest...)
}

func sortedKeys[T any](items map[string]T) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedNodeKeys(items map[string]*TypeNode) []string {
	if items == nil {
		return nil
	}
	return sortedKeys(items)
}

func jsonPath(parent, name string) string {
	if isIdentifier(name) {
		return parent + "." + name
	}
	return jsonMapPath(parent, name)
}

func jsonMapPath(parent, key string) string {
	quoted, _ := json.Marshal(key)
	return parent + "[" + string(quoted) + "]"
}

func isIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' {
			continue
		}
		if i > 0 && character >= '0' && character <= '9' {
			continue
		}
		return false
	}
	return true
}

func pathError(path, format string, args ...any) error {
	return fmt.Errorf("%s: %s", path, fmt.Sprintf(format, args...))
}
