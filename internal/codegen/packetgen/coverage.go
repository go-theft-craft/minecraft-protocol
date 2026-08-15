package packetgen

import (
	"encoding/json"
	"fmt"
	"sort"
)

// CoverageEntry is one packet the compiler produced a codec for.
//
// The report answers a question the generated Go cannot: which of the
// protocol's packets this repository actually reads and writes. A packet
// missing from it is missing from the build, and a diff of the report is the
// cheapest description of what a schema change did.
type CoverageEntry struct {
	State     string `json:"state"`
	Direction string `json:"direction"`
	ID        int32  `json:"id"`
	Name      string `json:"name"`
	GoType    string `json:"goType"`
}

// Coverage returns every generated packet, sorted by state, then direction,
// then ID, so two reports can be diffed line by line.
func Coverage(model *Model) ([]CoverageEntry, error) {
	if model == nil {
		return nil, fmt.Errorf("packetgen: nil model")
	}

	entries := make([]CoverageEntry, 0, modelPacketCount(model))
	for _, state := range model.States {
		for _, direction := range state.Directions {
			for _, packet := range direction.Packets {
				entries = append(entries, CoverageEntry{
					State:     state.SourceName,
					Direction: direction.SourceName,
					ID:        packet.ID,
					Name:      packet.SourceName,
					GoType:    packet.GoName,
				})
			}
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].State != entries[j].State {
			return entries[i].State < entries[j].State
		}
		if entries[i].Direction != entries[j].Direction {
			return entries[i].Direction < entries[j].Direction
		}

		return entries[i].ID < entries[j].ID
	})

	return entries, nil
}

// CoverageJSON renders the report as the checked-in file: indented, so a diff
// is one line per changed field, and newline-terminated like every other file
// in the tree.
func CoverageJSON(model *Model) ([]byte, error) {
	entries, err := Coverage(model)
	if err != nil {
		return nil, err
	}
	rendered, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("packetgen: render coverage: %w", err)
	}

	return append(rendered, '\n'), nil
}
