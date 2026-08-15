// Package current names the newest Java version this repository supports.
//
// It delegates and copies nothing: every function here returns what the
// version package returns. The name follows the newest supported version and
// offers no compatibility promise across releases — a program that must keep
// speaking one protocol should import that version's package by name, and one
// that wants whatever is newest should import this and expect it to move.
package current

import (
	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/data"
	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
)

// Version is the version this alias currently follows, as a stable
// registration key. It is here so a program can report what it is speaking
// without importing the version package it was trying to avoid naming.
const Version = "java/26.1"

// Protocol returns the current version's protocol descriptor.
func Protocol() protocol.Protocol { return v26_1.Protocol() }

// Data returns the current version's game data.
func Data() (*data.Set, error) { return v26_1.Data() }

// Raw returns the current version's source datasets as upstream published them.
func Raw() *data.RawSet { return v26_1.Raw() }
