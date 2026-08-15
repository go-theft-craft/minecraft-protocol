package java

// PacketValue is a protocol-version packet value encoded with mc struct tags.
// It is distinct from protocol.Packet, which is the decoded packet envelope.
type PacketValue interface {
	PacketID() int32
}

// Deref reads an optional value, reporting whether it was present.
//
// A ProtoDef switch may discriminate on an optional field, comparing against
// the value the option carries rather than against the option itself. An absent
// value matches no case and takes the default, so presence has to travel
// alongside the value rather than collapsing into a zero.
func Deref[T any](value *T) (T, bool) {
	if value == nil {
		var absent T

		return absent, false
	}

	return *value, true
}
