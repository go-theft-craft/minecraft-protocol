package v1_8

import (
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

// newRoleSession builds a client session for role lookups. Roles are a
// property of the protocol, not of the connection, so the role the session was
// created with does not change the answers.
func newRoleSession(t *testing.T) protocol.Session {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}
	session, err := Protocol().NewSession(protocol.RoleClient, limits)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}

	return session
}

func TestProtocol47ReportsLoginRoles(t *testing.T) {
	t.Parallel()

	roles, ok := newRoleSession(t).(protocol.LoginRoles)
	if !ok {
		t.Fatal("the generated session must implement protocol.LoginRoles")
	}

	cases := []struct {
		name      string
		state     protocol.State
		direction protocol.Direction
		id        int32
		want      protocol.LoginRole
	}{
		{
			name:      "encryption request",
			state:     StateLogin,
			direction: protocol.DirectionClientbound,
			id:        0x01,
			want:      protocol.RoleEncryptionRequest,
		},
		{
			name:      "encryption response",
			state:     StateLogin,
			direction: protocol.DirectionServerbound,
			id:        0x01,
			want:      protocol.RoleEncryptionResponse,
		},
		{
			name:      "login success",
			state:     StateLogin,
			direction: protocol.DirectionClientbound,
			id:        0x02,
			want:      protocol.RoleLoginSuccess,
		},
		{
			name:      "set compression",
			state:     StateLogin,
			direction: protocol.DirectionClientbound,
			id:        0x03,
			want:      protocol.RoleSetCompression,
		},
		{
			name:      "login start",
			state:     StateLogin,
			direction: protocol.DirectionServerbound,
			id:        0x00,
			want:      protocol.RoleLoginStart,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got, ok := roles.LoginRole(testCase.state, testCase.direction, testCase.id)
			if !ok {
				t.Fatalf("LoginRole(%q, %d, %#x) reported no role", testCase.state, testCase.direction, testCase.id)
			}
			if got != testCase.want {
				t.Fatalf("LoginRole = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestProtocol47ReportsNoRoleOutsideLogin(t *testing.T) {
	t.Parallel()

	roles, ok := newRoleSession(t).(protocol.LoginRoles)
	if !ok {
		t.Fatal("the generated session must implement protocol.LoginRoles")
	}

	cases := []struct {
		name      string
		state     protocol.State
		direction protocol.Direction
		id        int32
	}{
		{name: "a play packet", state: StatePlay, direction: protocol.DirectionClientbound, id: 0x01},
		{name: "the login disconnect", state: StateLogin, direction: protocol.DirectionClientbound, id: 0x00},
		{name: "an unassigned ID", state: StateLogin, direction: protocol.DirectionServerbound, id: 0x7f},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got, ok := roles.LoginRole(testCase.state, testCase.direction, testCase.id); ok {
				t.Fatalf("LoginRole reported %q, want no role", got)
			}
		})
	}
}
