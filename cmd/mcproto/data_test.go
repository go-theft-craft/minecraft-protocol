package main

import (
	"reflect"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/source/manifest"
)

// TestTreeReportNamesMeasuredDatasets pins that a dataset read out of a game
// jar is visible in the report.
//
// A measured dataset verifies exactly like an upstream one, which is why it
// would otherwise be invisible: the counts, the digests, and the validation all
// look the same. Where it came from is the one thing that differs, and a report
// that does not say so leaves a reader to open the manifest to find out.
func TestTreeReportNamesMeasuredDatasets(t *testing.T) {
	loaded, err := manifest.Load("../../source/java/1.8")
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}

	report := newTreeReport(loaded)
	if want := []string{"blockMovement", "physics"}; !reflect.DeepEqual(report.Measured, want) {
		t.Fatalf("measured datasets = %v, want %v", report.Measured, want)
	}
}

// TestTreeReportNamesTheMeasuredDatasetsOf26_1 pins the other real tree. It
// carries the block measurement and not the physics one, because 26.1.2 has a
// block dumper and no physics dumper yet.
func TestTreeReportNamesTheMeasuredDatasetsOf26_1(t *testing.T) {
	loaded, err := manifest.Load("../../source/java/26.1")
	if err != nil {
		t.Fatalf("manifest.Load: %v", err)
	}

	report := newTreeReport(loaded)
	if want := []string{"blockMovement"}; !reflect.DeepEqual(report.Measured, want) {
		t.Fatalf("measured datasets = %v, want %v", report.Measured, want)
	}
}

// TestTreeReportOmitsMeasuredDatasetsWhenThereAreNone pins the third case: a
// tree nobody has measured says nothing rather than reporting an empty list.
//
// It builds the manifest rather than loading one, because every tree in this
// repository is now measured and the distinction between "none" and "an empty
// list" would otherwise stop being tested the moment it stopped being visible.
func TestTreeReportOmitsMeasuredDatasetsWhenThereAreNone(t *testing.T) {
	unmeasured := &manifest.Manifest{
		Edition:                "java",
		TargetMinecraftVersion: "1.8.9",
		Protocol:               47,
	}

	if report := newTreeReport(unmeasured); report.Measured != nil {
		t.Fatalf("measured datasets = %v, want none", report.Measured)
	}
}
