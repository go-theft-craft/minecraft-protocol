package v26_1

import (
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

func newTestSession(t *testing.T, role protocol.Role, state protocol.State) protocol.Session {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}
	session, err := Protocol().NewSession(role, limits)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.SetState(state)

	return session
}

// TestProtocol775ProposeTransition walks the sequence a modern connection
// actually takes. Every state change is proposed by a serverbound packet,
// including the two the server starts: the client answers from the state it is
// still in, and both sides move on the answer.
func TestProtocol775ProposeTransition(t *testing.T) {
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
			value:        &HandshakingServerboundSetProtocol{ProtocolVersion: 775, NextState: 1},
			wantProposed: true,
			wantState:    StateStatus,
		},
		{
			name:         "handshake to login",
			state:        StateHandshaking,
			value:        &HandshakingServerboundSetProtocol{ProtocolVersion: 775, NextState: 2},
			wantProposed: true,
			wantState:    StateLogin,
		},
		{
			name:    "handshake to an unsupported next state",
			state:   StateHandshaking,
			value:   &HandshakingServerboundSetProtocol{ProtocolVersion: 775, NextState: 4},
			wantErr: true,
		},
		{
			name:         "login success proposes nothing",
			state:        StateLogin,
			value:        &LoginClientboundSuccess{},
			wantProposed: false,
		},
		{
			name:         "login acknowledged to configuration",
			state:        StateLogin,
			value:        &LoginServerboundLoginAcknowledged{},
			wantProposed: true,
			wantState:    StateConfiguration,
		},
		{
			name:    "login acknowledged outside login",
			state:   StatePlay,
			value:   &LoginServerboundLoginAcknowledged{},
			wantErr: true,
		},
		{
			name:         "finish configuration to play",
			state:        StateConfiguration,
			value:        &ConfigurationServerboundFinishConfiguration{},
			wantProposed: true,
			wantState:    StatePlay,
		},
		{
			name:    "finish configuration outside configuration",
			state:   StateLogin,
			value:   &ConfigurationServerboundFinishConfiguration{},
			wantErr: true,
		},
		{
			name:         "configuration acknowledged back to configuration",
			state:        StatePlay,
			value:        &PlayServerboundConfigurationAcknowledged{},
			wantProposed: true,
			wantState:    StateConfiguration,
		},
		{
			name:         "start configuration proposes nothing on its own",
			state:        StatePlay,
			value:        &PlayClientboundStartConfiguration{},
			wantProposed: false,
		},
		{
			name:          "set compression during login",
			state:         StateLogin,
			value:         &LoginClientboundCompress{Threshold: 256},
			wantProposed:  true,
			wantControl:   true,
			wantEnabled:   true,
			wantThreshold: 256,
		},
		{
			name:         "set compression disabled",
			state:        StateLogin,
			value:        &LoginClientboundCompress{Threshold: -1},
			wantProposed: true,
			wantControl:  true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			session := newTestSession(t, protocol.RoleClient, test.state)
			proposer, ok := session.(interface {
				ProposeTransition(protocol.Packet) (protocol.Transition, bool, error)
			})
			if !ok {
				t.Fatal("the session does not propose transitions")
			}

			transition, proposed, err := proposer.ProposeTransition(protocol.Packet{Value: test.value})
			if test.wantErr {
				if err == nil {
					t.Fatal("ProposeTransition accepted a packet from the wrong state")
				}

				return
			}
			if err != nil {
				t.Fatalf("ProposeTransition: %v", err)
			}
			if proposed != test.wantProposed {
				t.Fatalf("proposed = %t, want %t", proposed, test.wantProposed)
			}
			if !proposed {
				return
			}
			if test.wantState != "" {
				if transition.State == nil {
					t.Fatalf("transition proposes no state, want %q", test.wantState)
				}
				if *transition.State != test.wantState {
					t.Errorf("state = %q, want %q", *transition.State, test.wantState)
				}
			}
			if !test.wantControl {
				return
			}
			control, ok := transition.Control.(java.CompressionControl)
			if !ok {
				t.Fatalf("control = %T, want a compression control", transition.Control)
			}
			if control.Enabled != test.wantEnabled || control.Threshold != test.wantThreshold {
				t.Errorf("control = {Enabled:%t Threshold:%d}, want {Enabled:%t Threshold:%d}",
					control.Enabled, control.Threshold, test.wantEnabled, test.wantThreshold)
			}
		})
	}
}

// TestProtocol775DisconnectPerState covers the encoding split: login carries a
// JSON string and the states after it carry an NBT component.
func TestProtocol775DisconnectPerState(t *testing.T) {
	t.Parallel()

	for _, state := range []protocol.State{StateLogin, StateConfiguration, StatePlay} {
		t.Run(string(state), func(t *testing.T) {
			t.Parallel()

			session := newTestSession(t, protocol.RoleServer, state)
			disconnector, ok := session.(interface {
				Disconnect(string) (protocol.Packet, bool, error)
			})
			if !ok {
				t.Fatal("the session cannot disconnect")
			}
			packet, sent, err := disconnector.Disconnect("closing")
			if err != nil {
				t.Fatalf("Disconnect: %v", err)
			}
			if !sent {
				t.Fatal("Disconnect produced no packet")
			}
			if packet.State != state {
				t.Errorf("packet state = %q, want %q", packet.State, state)
			}
		})
	}
}
