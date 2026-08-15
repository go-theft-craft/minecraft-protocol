package packetgen

import (
	"sort"
	"strings"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
)

// SharedType is one named schema type compiled once for the whole protocol
// rather than inlined into each packet that uses it.
//
// Sharing is not only about output size. A recursive type cannot be inlined at
// all -- inlining it does not terminate -- and protocol 775's slot, slot
// component, and item predicate refer to each other in a cycle. Sharing is what
// makes those compilable, and the byte-identical result for protocol 47 is what
// proves the mechanism is sound before that milestone needs it.
type SharedType struct {
	SchemaName string
	GoName     string
	// Declaration is the Go type this schema type compiles to.
	Declaration Declaration
	// Recursive reports that the type reaches itself, directly or through
	// other shared types.
	Recursive bool
	// GoType is the underlying type the schema definition compiled to. It
	// equals GoName when the definition produced its own declaration, and is
	// something like []Item when it did not.
	GoType string
	Decode []Operation
	Encode []Operation
}

// sharingPlan records which named types are compiled once and why.
type sharingPlan struct {
	shared map[string]bool
	// recursive marks the members of a non-trivial strongly connected
	// component and any self-referring type.
	recursive map[string]bool
	// order is the deterministic compilation order: sorted schema names.
	order []string
}

// planSharedTypes decides which named types to compile once.
//
// A type is shared when it is recursive, when it participates in a cycle, or
// when two or more packets refer to it. A type used by exactly one packet is
// left inlined, because naming it would add a Go type nothing else can hold
// while making the packet's own decoder harder to read.
func planSharedTypes(
	schema *protodef.Schema,
	direction protodef.Direction,
	natives map[string]struct{},
) sharingPlan {
	definitions := namedDefinitions(schema, direction)
	references := make(map[string]map[string]struct{}, len(definitions))
	for name, node := range definitions {
		references[name] = namedReferences(node, definitions)
	}

	users := make(map[string]int, len(definitions))
	for _, packet := range direction.Packets {
		for name := range reachableNames(packet.Type, definitions, references) {
			users[name]++
		}
	}

	recursive := recursiveNames(references)

	plan := sharingPlan{
		shared:    make(map[string]bool, len(definitions)),
		recursive: recursive,
	}
	for name := range definitions {
		// A parameterized type cannot be compiled once: its shape depends on
		// the argument each invocation supplies, so it has no single Go type
		// to share. Those stay inlined per use.
		if parameterDependent(definitions[name]) {
			continue
		}
		// A name that resolves to a scalar has no structure to share. `string`
		// is defined as an invocation of the native pstring, and giving it a
		// declared Go type would turn every string field into a named type and
		// break switch cases that compare against a Go string.
		if resolvesToCodec(natives, definitions, definitions[name]) {
			continue
		}
		if recursive[name] || users[name] >= 2 {
			plan.shared[name] = true
			plan.order = append(plan.order, name)
		}
	}
	sort.Strings(plan.order)

	return plan
}

// namedDefinitions collects the named types a direction can reference: the
// protocol-wide types plus the direction's own, with the direction winning.
// Packet body types are excluded; they are compiled as packets.
func namedDefinitions(schema *protodef.Schema, direction protodef.Direction) map[string]*protodef.TypeNode {
	definitions := make(map[string]*protodef.TypeNode)
	if schema != nil {
		for name, node := range schema.Types {
			if isNativeDefinition(name, node) {
				continue
			}
			definitions[name] = node
		}
	}
	for name, node := range direction.Types {
		if isNativeDefinition(name, node) {
			continue
		}
		definitions[name] = node
	}
	for _, packet := range direction.Packets {
		delete(definitions, packet.TypeName)
	}
	delete(definitions, "packet")

	return definitions
}

// isNativeDefinition reports whether a definition is the native marker itself
// rather than a definition that happens to invoke a native.
//
// `entityMetadata` is defined as an invocation of the native
// `entityMetadataLoop`, so its stored node is a native node. Treating that as a
// native definition would exclude it from sharing even though it is an ordinary
// schema type with three users.
func isNativeDefinition(name string, node *protodef.TypeNode) bool {
	return node == nil || (node.Kind == protodef.KindNative && node.Name == name)
}

// namedReferences returns the named types a node refers to directly, without
// descending through those names.
func namedReferences(node *protodef.TypeNode, definitions map[string]*protodef.TypeNode) map[string]struct{} {
	found := make(map[string]struct{})
	walkTypeNode(node, func(current *protodef.TypeNode) bool {
		if current.Kind != protodef.KindAlias {
			return true
		}
		if _, defined := definitions[current.Name]; !defined {
			return true
		}
		found[current.Name] = struct{}{}

		// Stop at the name: the reference is what matters here, and
		// descending would conflate one type's references with another's.
		return false
	})

	return found
}

// reachableNames returns every named type a packet body reaches, following
// references transitively so a type used once directly and once through
// another type counts for both.
func reachableNames(
	node *protodef.TypeNode,
	definitions map[string]*protodef.TypeNode,
	references map[string]map[string]struct{},
) map[string]struct{} {
	reached := make(map[string]struct{})
	pending := make([]string, 0, len(definitions))
	for name := range namedReferences(node, definitions) {
		pending = append(pending, name)
	}
	for len(pending) > 0 {
		name := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		if _, seen := reached[name]; seen {
			continue
		}
		reached[name] = struct{}{}
		for next := range references[name] {
			pending = append(pending, next)
		}
	}

	return reached
}

// recursiveNames returns every name that reaches itself through the reference
// graph, which covers both self-recursion and mutual recursion.
//
// This is reachability rather than a strongly-connected-component algorithm.
// The two agree on the question being asked -- does this name reach itself --
// and the graph has a few dozen nodes, so the simpler one is the right one.
func recursiveNames(references map[string]map[string]struct{}) map[string]bool {
	recursive := make(map[string]bool, len(references))
	for start := range references {
		seen := make(map[string]struct{}, len(references))
		pending := []string{start}
		for len(pending) > 0 {
			name := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			for next := range references[name] {
				if next == start {
					recursive[start] = true
					pending = nil

					break
				}
				if _, visited := seen[next]; visited {
					continue
				}
				seen[next] = struct{}{}
				pending = append(pending, next)
			}
		}
	}

	return recursive
}

// resolvesToCodec reports whether a definition ends at a hand-written codec,
// following alias references to their targets.
func resolvesToCodec(
	natives map[string]struct{},
	definitions map[string]*protodef.TypeNode,
	node *protodef.TypeNode,
) bool {
	for range len(definitions) + 1 {
		if node == nil {
			return false
		}
		if _, structural := structuralNatives[node.Name]; structural {
			return false
		}
		if _, ok := basicScalarRules[node.Name]; ok {
			return true
		}
		if _, native := natives[node.Name]; native {
			_, hasCodec := nativeCodecs[node.Name]

			return hasCodec
		}
		if node.Kind != protodef.KindAlias {
			return false
		}
		if target, defined := definitions[node.Name]; defined && target != node {
			node = target

			continue
		}
		node = node.Target
	}

	return false
}

// parameterDependent reports whether a definition reads one of its own
// parameters, which makes it uncompilable outside an invocation that binds it.
func parameterDependent(node *protodef.TypeNode) bool {
	dependent := false
	walkTypeNode(node, func(current *protodef.TypeNode) bool {
		// An invocation that supplies arguments binds whatever parameters the
		// target reads, so the target's own `$` references are satisfied and
		// do not make this definition parameter-dependent.
		if current != node && current.Kind == protodef.KindAlias && len(current.Arguments) != 0 {
			return false
		}
		if strings.HasPrefix(current.CompareTo, "$") {
			dependent = true
		}
		if current.Count != nil && strings.HasPrefix(current.Count.Reference, "$") {
			dependent = true
		}

		return !dependent
	})

	return dependent
}

// walkTypeNode visits node and its children. The visitor returns false to stop
// descending into that node's children.
//
// A recursive schema is a cyclic graph, not a tree: an alias node's target can
// reach the alias again. The visited set is what makes the walk terminate on
// exactly the schemas this milestone exists to support.
func walkTypeNode(node *protodef.TypeNode, visit func(*protodef.TypeNode) bool) {
	walkTypeNodeSeen(node, visit, map[*protodef.TypeNode]struct{}{})
}

func walkTypeNodeSeen(
	node *protodef.TypeNode,
	visit func(*protodef.TypeNode) bool,
	seen map[*protodef.TypeNode]struct{},
) {
	if node == nil {
		return
	}
	if _, visited := seen[node]; visited {
		return
	}
	seen[node] = struct{}{}
	if !visit(node) {
		return
	}

	walkTypeNodeSeen(node.Target, visit, seen)
	walkTypeNodeSeen(node.Element, visit, seen)
	walkTypeNodeSeen(node.Default, visit, seen)
	if node.Count != nil {
		walkTypeNodeSeen(node.Count.Type, visit, seen)
	}
	for _, field := range node.Fields {
		walkTypeNodeSeen(field.Type, visit, seen)
	}
	for _, switchCase := range node.Cases {
		walkTypeNodeSeen(switchCase.Type, visit, seen)
	}
	for _, argument := range node.Arguments {
		walkTypeNodeSeen(argument.Type, visit, seen)
	}
}
