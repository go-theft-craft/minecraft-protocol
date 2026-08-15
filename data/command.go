package data

import "slices"

// CommandNodeType names the part a node plays in the command tree.
type CommandNodeType string

const (
	// CommandNodeRoot is the single node every command hangs from.
	CommandNodeRoot CommandNodeType = "root"
	// CommandNodeLiteral is a fixed word, such as "give".
	CommandNodeLiteral CommandNodeType = "literal"
	// CommandNodeArgument is a value parsed by the node's parser.
	CommandNodeArgument CommandNodeType = "argument"
)

// CommandNode is one node of the Brigadier command tree.
type CommandNode struct {
	Type CommandNodeType
	Name string
	// Executable reports whether a command may end at this node.
	Executable bool
	// Redirects names the nodes this one continues into, by name. It is how
	// upstream flattens the graph back into a tree.
	Redirects []string
	Children  CommandNodes
	// Parser is present on argument nodes and nil on the rest.
	Parser *CommandParser
}

// Clone returns a CommandNode whose mutable fields do not alias the source.
func (c CommandNode) Clone() CommandNode {
	clone := c
	clone.Redirects = slices.Clone(c.Redirects)
	clone.Children = c.Children.Clone()
	if c.Parser != nil {
		parser := c.Parser.Clone()
		clone.Parser = &parser
	}

	return clone
}

// CommandNodes is a collection of command tree nodes.
type CommandNodes []CommandNode

// Clone returns command nodes whose mutable fields do not alias the source.
func (c CommandNodes) Clone() CommandNodes {
	if c == nil {
		return nil
	}

	clone := make(CommandNodes, len(c))
	for index := range clone {
		clone[index] = c[index].Clone()
	}

	return clone
}

// CommandParser names the Brigadier parser that reads an argument, together
// with the properties that configure it.
type CommandParser struct {
	// Name is the parser's namespaced identifier, such as "brigadier:string".
	Name string
	// Modifier configures the parser. It is nil when upstream publishes none.
	Modifier *CommandParserModifier
	// Examples are upstream's sample values for the parser. They appear in the
	// parser catalogue and not on tree nodes.
	Examples []string
}

// Clone returns a CommandParser whose mutable fields do not alias the source.
func (c CommandParser) Clone() CommandParser {
	clone := c
	clone.Examples = slices.Clone(c.Examples)
	if c.Modifier != nil {
		modifier := c.Modifier.Clone()
		clone.Modifier = &modifier
	}

	return clone
}

// CommandParsers is a collection of command argument parsers.
type CommandParsers []CommandParser

// Clone returns command parsers whose mutable fields do not alias the source.
func (c CommandParsers) Clone() CommandParsers {
	if c == nil {
		return nil
	}

	clone := make(CommandParsers, len(c))
	for index := range clone {
		clone[index] = c[index].Clone()
	}

	return clone
}

// CommandParserModifier configures a Brigadier parser. Its fields are the
// closed set upstream publishes: a kind, how many targets a selector may
// match, the registry a resource is drawn from, and numeric bounds. A field a
// parser does not configure is empty or nil.
type CommandParserModifier struct {
	Type     string
	Amount   string
	Registry string
	Min      *float64
	Max      *float64
}

// Clone returns a modifier whose mutable fields do not alias the source.
func (c CommandParserModifier) Clone() CommandParserModifier {
	clone := c
	if c.Min != nil {
		lower := *c.Min
		clone.Min = &lower
	}
	if c.Max != nil {
		upper := *c.Max
		clone.Max = &upper
	}

	return clone
}

// CommandTree is the command graph a server publishes, together with the
// catalogue of parsers its argument nodes draw on.
type CommandTree struct {
	Root    CommandNode
	Parsers CommandParsers
}

// Clone returns a CommandTree whose mutable fields do not alias the source.
func (c CommandTree) Clone() CommandTree {
	clone := c
	clone.Root = c.Root.Clone()
	clone.Parsers = c.Parsers.Clone()

	return clone
}
