package main

import (
	"context"
	"fmt"
	"net"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/protocols"
)

// The states these commands name directly. Both protocols use these names, and
// naming them here rather than importing a generated package is what keeps the
// commands version-neutral.
const stateStatus = protocol.State("status")

// The next-state values a handshake carries. They are protocol constants, not
// choices this tool makes.
const (
	statusNextState = 1
	loginNextState  = 2
)

// startStream builds a client session and a started stream over one
// connection.
func startStream(
	ctx context.Context,
	descriptor protocol.Protocol,
	conn net.Conn,
	options ...protocol.StreamOption,
) (*protocol.Stream, protocol.Session, error) {
	limits, err := protocol.NewLimits()
	if err != nil {
		return nil, nil, fmt.Errorf("limits: %w", err)
	}
	session, err := descriptor.NewSession(protocol.RoleClient, limits)
	if err != nil {
		return nil, nil, fmt.Errorf("session: %w", err)
	}

	stream, err := protocol.NewStream(session, protocol.Transport{
		Reader:    conn,
		Writer:    conn,
		Interrupt: conn.Close,
	}, options...)
	if err != nil {
		return nil, nil, fmt.Errorf("stream: %w", err)
	}
	if err := stream.Start(ctx); err != nil {
		return nil, nil, fmt.Errorf("start stream: %w", err)
	}

	return stream, session, nil
}

// writeHandshake sends the one packet every connection opens with.
func writeHandshake(
	ctx context.Context,
	stream *protocol.Stream,
	descriptor protocol.Protocol,
	host string,
	port uint16,
	nextState int32,
) error {
	packet, err := protocols.Handshake(descriptor, host, port, nextState)
	if err != nil {
		return err
	}
	if err := stream.Write(ctx, packet); err != nil {
		return peerf("write handshake: %w", err)
	}

	return nil
}
