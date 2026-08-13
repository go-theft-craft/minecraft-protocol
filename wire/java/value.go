package java

// PacketValue is a protocol-version packet value encoded with mc struct tags.
// It is distinct from protocol.Packet, which is the decoded packet envelope.
type PacketValue interface {
	PacketID() int32
}
