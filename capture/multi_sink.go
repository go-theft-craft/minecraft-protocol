package capture

import (
	"context"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// multiSink delivers each observation to several sinks in order.
type multiSink []protocol.ObservationSink

// MultiSink composes sinks, calling them in the order given.
//
// The first failure stops the rest and is returned. That is not a policy
// choice this package is free to make differently: an observation failure
// terminates the stream, so the sinks after the failing one would be doing
// work on behalf of a connection that is already ending.
func MultiSink(sinks ...protocol.ObservationSink) (protocol.ObservationSink, error) {
	if len(sinks) == 0 {
		return nil, fmt.Errorf("%w: no sinks to compose", ErrInvalidCapture)
	}
	for index, sink := range sinks {
		if sink == nil {
			return nil, fmt.Errorf("%w: sink %d is nil", ErrInvalidCapture, index)
		}
	}

	composed := make(multiSink, len(sinks))
	copy(composed, sinks)

	return composed, nil
}

// Observe implements protocol.ObservationSink.
func (m multiSink) Observe(ctx context.Context, observation protocol.Observation) error {
	for _, sink := range m {
		if err := sink.Observe(ctx, observation); err != nil {
			return err
		}
	}

	return nil
}
