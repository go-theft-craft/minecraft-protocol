package protocol

// LoginRole reports the part a packet plays in this stream's login sequence.
//
// It reads no session state: a generated session answers from a table built at
// package initialization, so this is safe to call while the stream runs. A
// protocol with no login sequence reports no role.
func (s *Stream) LoginRole(state State, direction Direction, id int32) (LoginRole, bool) {
	roles, ok := s.session.(LoginRoles)
	if !ok {
		return "", false
	}

	return roles.LoginRole(state, direction, id)
}

// LoginExchange returns this stream's login exchange, when its protocol has a
// login sequence.
//
// The exchange is stateless, which is what makes reaching for it here safe
// while the coordinator owns the session: it builds and reads packets and
// touches nothing the coordinator mutates.
func (s *Stream) LoginExchange() (LoginExchange, bool) {
	exchanges, ok := s.session.(LoginExchanges)
	if !ok {
		return nil, false
	}

	return exchanges.LoginExchange()
}
