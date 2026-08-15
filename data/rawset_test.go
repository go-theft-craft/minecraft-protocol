package data_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/data"
)

func sampleRawSet(t *testing.T) *data.RawSet {
	t.Helper()

	set, err := data.NewRawSet(
		data.Version{Protocol: 775, MinecraftVersion: "26.1", MajorVersion: "26.1"},
		[]data.RawDataset{
			{Name: "sounds", Path: "data/sounds.json", MediaType: "application/json", Data: []byte(`[{"id":0}]`)},
			{Name: "blockLoot", Path: "data/blockLoot.json", MediaType: "application/json", Data: []byte(`[]`)},
			{Name: "tints", Path: "data/tints.json", MediaType: "application/json", Data: []byte(`{}`)},
		},
	)
	if err != nil {
		t.Fatalf("NewRawSet: %v", err)
	}

	return set
}

func TestRawSetNamesAreSorted(t *testing.T) {
	set := sampleRawSet(t)

	names := set.Names()
	want := []string{"blockLoot", "sounds", "tints"}
	if !slices.Equal(names, want) {
		t.Errorf("Names() = %v, want %v", names, want)
	}
	if set.Len() != len(want) {
		t.Errorf("Len() = %d, want %d", set.Len(), len(want))
	}

	// The caller owns the slice, so editing it cannot reorder the set.
	names[0] = "zzz"
	if got := set.Names(); !slices.Equal(got, want) {
		t.Errorf("Names() = %v after the caller edited its copy, want %v", got, want)
	}
}

func TestRawSetGetReturnsACopy(t *testing.T) {
	set := sampleRawSet(t)

	dataset, ok := set.Get("sounds")
	if !ok {
		t.Fatal("sounds is missing")
	}
	if string(dataset.Data) != `[{"id":0}]` {
		t.Fatalf("Data = %q", dataset.Data)
	}

	// The bytes are the pinned upstream record. A caller editing them must not
	// change what the next reader sees.
	dataset.Data[0] = 'X'

	again, ok := set.Get("sounds")
	if !ok {
		t.Fatal("sounds is missing on the second read")
	}
	if string(again.Data) != `[{"id":0}]` {
		t.Errorf("Data = %q after the caller edited its copy", again.Data)
	}
}

func TestRawSetReportsWhatItHolds(t *testing.T) {
	set := sampleRawSet(t)

	if !set.Has("tints") {
		t.Error("Has(tints) = false")
	}
	if set.Has("absent") {
		t.Error("Has(absent) = true")
	}
	if _, ok := set.Get("absent"); ok {
		t.Error("Get(absent) reported a dataset")
	}
	if got := set.Version().Protocol; got != 775 {
		t.Errorf("Version().Protocol = %d, want 775", got)
	}
}

func TestNewRawSetRejectsBadInventories(t *testing.T) {
	cases := []struct {
		name     string
		datasets []data.RawDataset
	}{
		{name: "an empty name", datasets: []data.RawDataset{{Name: ""}}},
		{
			name:     "a duplicate name",
			datasets: []data.RawDataset{{Name: "sounds"}, {Name: "sounds"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := data.NewRawSet(data.Version{}, tc.datasets)
			if err == nil {
				t.Fatal("NewRawSet accepted the inventory")
			}
			if !errors.Is(err, data.ErrInvalidDataset) {
				t.Errorf("error = %v, want ErrInvalidDataset", err)
			}
		})
	}
}

// TestRawSetIsUsableWhenNil keeps a version that ships no raw datasets from
// making every caller nil-check before asking.
func TestRawSetIsUsableWhenNil(t *testing.T) {
	var set *data.RawSet

	if set.Len() != 0 || set.Names() != nil || set.Has("sounds") {
		t.Error("a nil RawSet reported contents")
	}
	if _, ok := set.Get("sounds"); ok {
		t.Error("a nil RawSet returned a dataset")
	}
	if set.Version() != (data.Version{}) {
		t.Error("a nil RawSet reported a version")
	}
}
