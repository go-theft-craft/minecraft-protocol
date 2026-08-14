package v1_8

import (
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

func TestProtocol47ProposeTransition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		state         protocol.State
		value         any
		wantProposed  bool
		wantErr       bool
		wantState     protocol.State
		wantControl   bool
		wantEnabled   bool
		wantThreshold int32
	}{
		{
			name:         "handshake to status",
			state:        StateHandshaking,
			value:        &HandshakingServerboundSetProtocol{ProtocolVersion: 47, NextState: 1},
			wantProposed: true,
			wantState:    StateStatus,
		},
		{
			name:         "handshake to login",
			state:        StateHandshaking,
			value:        &HandshakingServerboundSetProtocol{ProtocolVersion: 47, NextState: 2},
			wantProposed: true,
			wantState:    StateLogin,
		},
		{
			name:    "handshake to unsupported state",
			state:   StateHandshaking,
			value:   &HandshakingServerboundSetProtocol{ProtocolVersion: 47, NextState: 3},
			wantErr: true,
		},
		{
			name:    "handshake to negative state",
			state:   StateHandshaking,
			value:   &HandshakingServerboundSetProtocol{ProtocolVersion: 47, NextState: -1},
			wantErr: true,
		},
		{
			name:    "handshake in the wrong state",
			state:   StateStatus,
			value:   &HandshakingServerboundSetProtocol{ProtocolVersion: 47, NextState: 1},
			wantErr: true,
		},
		{
			name:         "login success to play",
			state:        StateLogin,
			value:        &LoginClientboundSuccess{UUID: "id", Username: "Alex"},
			wantProposed: true,
			wantState:    StatePlay,
		},
		{
			name:    "login success in the wrong state",
			state:   StatePlay,
			value:   &LoginClientboundSuccess{},
			wantErr: true,
		},
		{
			name:          "login compression enabled",
			state:         StateLogin,
			value:         &LoginClientboundCompress{Threshold: 256},
			wantProposed:  true,
			wantControl:   true,
			wantEnabled:   true,
			wantThreshold: 256,
		},
		{
			name:         "login compression disabled by negative threshold",
			state:        StateLogin,
			value:        &LoginClientboundCompress{Threshold: -1},
			wantProposed: true,
			wantControl:  true,
		},
		{
			name:         "login compression threshold zero",
			state:        StateLogin,
			value:        &LoginClientboundCompress{Threshold: 0},
			wantProposed: true,
			wantControl:  true,
			wantEnabled:  true,
		},
		{
			name:    "login compression in the wrong state",
			state:   StatePlay,
			value:   &LoginClientboundCompress{Threshold: 8},
			wantErr: true,
		},
		{
			name:          "play compression enabled",
			state:         StatePlay,
			value:         &PlayClientboundSetCompression{Threshold: 64},
			wantProposed:  true,
			wantControl:   true,
			wantEnabled:   true,
			wantThreshold: 64,
		},
		{
			name:         "play compression disabled by negative threshold",
			state:        StatePlay,
			value:        &PlayClientboundSetCompression{Threshold: -5},
			wantProposed: true,
			wantControl:  true,
		},
		{
			name:  "unrelated packet proposes nothing",
			state: StatePlay,
			value: &PlayServerboundChat{Message: "hello"},
		},
		{
			name:  "unknown packet proposes nothing",
			state: StateStatus,
			value: protocol.UnknownPacket{Payload: []byte{1}},
		},
		{
			name:  "raw packet proposes nothing",
			state: StateStatus,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			// Both roles run the same rules: direction decides only whether
			// the trigger follows an inbound decode or an outbound write.
			for _, role := range []protocol.Role{protocol.RoleClient, protocol.RoleServer} {
				session := newTestSession(t, role, testCase.state)
				before := session.Snapshot()

				transition, proposed, err := session.ProposeTransition(protocol.Packet{Value: testCase.value})

				if testCase.wantErr {
					if err == nil {
						t.Fatalf("role %d: ProposeTransition() error = nil, want an error", role)
					}
					if proposed {
						t.Errorf("role %d: ProposeTransition() proposed a transition alongside an error", role)
					}
				} else {
					if err != nil {
						t.Fatalf("role %d: ProposeTransition() error = %v", role, err)
					}
					if proposed != testCase.wantProposed {
						t.Fatalf("role %d: ProposeTransition() proposed = %t, want %t", role, proposed, testCase.wantProposed)
					}
				}

				if testCase.wantState != "" {
					if transition.State == nil || *transition.State != testCase.wantState {
						t.Errorf("role %d: transition state = %v, want %q", role, transition.State, testCase.wantState)
					}
				} else if transition.State != nil {
					t.Errorf("role %d: transition state = %q, want none", role, *transition.State)
				}

				if testCase.wantControl {
					control, ok := transition.Control.(java.CompressionControl)
					if !ok {
						t.Fatalf("role %d: transition control = %T, want java.CompressionControl", role, transition.Control)
					}
					if control.Enabled != testCase.wantEnabled || control.Threshold != testCase.wantThreshold {
						t.Errorf("role %d: control = %+v, want enabled %t threshold %d", role, control, testCase.wantEnabled, testCase.wantThreshold)
					}
					if control.Policy != java.StrictCompression {
						t.Errorf("role %d: control policy = %v, want the session policy to survive", role, control.Policy)
					}
				} else if transition.Control != nil {
					t.Errorf("role %d: transition control = %v, want none", role, transition.Control)
				}

				// Proposing must never mutate the session.
				after := session.Snapshot()
				if after.State != before.State {
					t.Errorf("role %d: ProposeTransition() changed state from %q to %q", role, before.State, after.State)
				}
				for key, value := range before.Pipeline {
					if after.Pipeline[key] != value {
						t.Errorf("role %d: ProposeTransition() changed pipeline[%q] from %q to %q", role, key, value, after.Pipeline[key])
					}
				}
			}
		})
	}
}

func TestProtocol47TransitionApplyIsAtomic(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, protocol.RoleClient, StateLogin)

	transition, proposed, err := session.ProposeTransition(protocol.Packet{
		Value: &LoginClientboundCompress{Threshold: 128},
	})
	if err != nil || !proposed {
		t.Fatalf("ProposeTransition() = %+v, %t, %v", transition, proposed, err)
	}
	if err := session.ValidateTransition(transition); err != nil {
		t.Fatalf("ValidateTransition() error = %v", err)
	}
	session.ApplyTransition(transition)

	snapshot := session.Snapshot()
	if snapshot.State != StateLogin {
		t.Errorf("state = %q, want it unchanged at %q", snapshot.State, StateLogin)
	}
	if snapshot.Pipeline["compression.enabled"] != "true" || snapshot.Pipeline["compression.threshold"] != "128" {
		t.Errorf("pipeline = %v, want compression enabled at 128", snapshot.Pipeline)
	}
}

func TestProtocol47TransitionAppliesStateAndControlTogether(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, protocol.RoleServer, StateLogin)

	state := StatePlay
	transition := protocol.Transition{
		State:   &state,
		Control: java.CompressionControl{Enabled: true, Threshold: 16, Policy: java.CompatibleCompression},
	}
	if err := session.ValidateTransition(transition); err != nil {
		t.Fatalf("ValidateTransition() error = %v", err)
	}
	session.ApplyTransition(transition)

	snapshot := session.Snapshot()
	if snapshot.State != StatePlay {
		t.Errorf("state = %q, want %q", snapshot.State, StatePlay)
	}
	if snapshot.Pipeline["compression.threshold"] != "16" || snapshot.Pipeline["compression.policy"] != "compatible" {
		t.Errorf("pipeline = %v, want the control applied too", snapshot.Pipeline)
	}
}

func TestProtocol47ValidateTransitionRejectsBadTargets(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, protocol.RoleServer, StateLogin)

	if err := session.ValidateTransition(protocol.Transition{}); err != nil {
		t.Fatalf("ValidateTransition(empty) error = %v", err)
	}

	unsupported := protocol.State("configuration")
	if err := session.ValidateTransition(protocol.Transition{State: &unsupported}); err == nil {
		t.Error("ValidateTransition(unsupported state) error = nil")
	}
	bad := protocol.Transition{Control: java.CompressionControl{Enabled: true, Threshold: -1, Policy: java.StrictCompression}}
	if err := session.ValidateTransition(bad); err == nil {
		t.Error("ValidateTransition(invalid control) error = nil")
	}
}

func TestProtocol47DisconnectCapability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		role      protocol.Role
		state     protocol.State
		supported bool
		wantID    int32
		wantType  any
	}{
		{
			name: "server login", role: protocol.RoleServer, state: StateLogin,
			supported: true, wantID: 0x00, wantType: &LoginClientboundDisconnect{},
		},
		{
			name: "server play", role: protocol.RoleServer, state: StatePlay,
			supported: true, wantID: 0x40, wantType: &PlayClientboundKickDisconnect{},
		},
		{name: "server handshaking", role: protocol.RoleServer, state: StateHandshaking},
		{name: "server status", role: protocol.RoleServer, state: StateStatus},
		{name: "client login", role: protocol.RoleClient, state: StateLogin},
		{name: "client play", role: protocol.RoleClient, state: StatePlay},
	}

	const reason = `{"text":"bye"}`

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			session := newTestSession(t, testCase.role, testCase.state)
			packet, supported, err := session.Disconnect(reason)
			if err != nil {
				t.Fatalf("Disconnect() error = %v", err)
			}
			if supported != testCase.supported {
				t.Fatalf("Disconnect() supported = %t, want %t", supported, testCase.supported)
			}
			if !testCase.supported {
				return
			}

			if packet.State != testCase.state || packet.Direction != session.Outbound() || packet.ID != testCase.wantID {
				t.Fatalf("Disconnect() envelope = %+v", packet)
			}
			switch value := packet.Value.(type) {
			case *LoginClientboundDisconnect:
				if value.Reason != reason {
					t.Errorf("reason = %q, want %q", value.Reason, reason)
				}
			case *PlayClientboundKickDisconnect:
				if value.Reason != reason {
					t.Errorf("reason = %q, want %q", value.Reason, reason)
				}
			default:
				t.Fatalf("Disconnect() value = %T, want %T", packet.Value, testCase.wantType)
			}

			// The disconnect packet must survive the normal outbound path.
			if _, err := session.EncodeFrame(packet); err != nil {
				t.Fatalf("EncodeFrame(disconnect) error = %v", err)
			}
		})
	}
}

func TestProtocol47DisconnectRejectsOversizedReason(t *testing.T) {
	t.Parallel()

	limits, err := protocol.NewLimits(protocol.MaxStringBytes(16))
	if err != nil {
		t.Fatal(err)
	}
	session, err := Protocol().NewSession(protocol.RoleServer, limits)
	if err != nil {
		t.Fatal(err)
	}
	session.SetState(StateLogin)

	packet, supported, err := session.Disconnect(string(make([]byte, 64)))
	if err != nil || !supported {
		t.Fatalf("Disconnect() = %t, %v", supported, err)
	}
	if _, err := session.EncodeFrame(packet); err == nil {
		t.Fatal("EncodeFrame(oversized reason) error = nil, want the string limit to reject it")
	}
}
