package middleware_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/middleware"
)

// recordingSender records the packets that reached the base of a send chain.
type recordingSender struct {
	packets []protocol.Packet
	err     error
}

func (s *recordingSender) Send(_ context.Context, packet protocol.Packet) error {
	if s.err != nil {
		return s.err
	}
	s.packets = append(s.packets, packet)

	return nil
}

// recordingHandler is the receive half of recordingSender.
type recordingHandler struct {
	packets []protocol.Packet
	err     error
}

func (h *recordingHandler) Handle(_ context.Context, packet protocol.Packet) error {
	if h.err != nil {
		return h.err
	}
	h.packets = append(h.packets, packet)

	return nil
}

// noteSend returns a middleware that appends its name to order before and
// after calling the next sender, so a test can read the nesting off the trace.
func noteSend(order *[]string, name string) middleware.SendMiddleware {
	return func(next middleware.Sender) middleware.Sender {
		return middleware.SenderFunc(func(ctx context.Context, packet protocol.Packet) error {
			*order = append(*order, "enter "+name)
			err := next.Send(ctx, packet)
			*order = append(*order, "leave "+name)

			return err
		})
	}
}

func noteReceive(order *[]string, name string) middleware.ReceiveMiddleware {
	return func(next middleware.Handler) middleware.Handler {
		return middleware.HandlerFunc(func(ctx context.Context, packet protocol.Packet) error {
			*order = append(*order, "enter "+name)
			err := next.Handle(ctx, packet)
			*order = append(*order, "leave "+name)

			return err
		})
	}
}

func TestSendMiddlewareNestsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	var order []string
	base := &recordingSender{}

	chain, err := middleware.ChainSend(
		base,
		noteSend(&order, "first"),
		noteSend(&order, "second"),
		noteSend(&order, "third"),
	)
	if err != nil {
		t.Fatalf("ChainSend: %v", err)
	}
	if err := chain.Send(t.Context(), protocol.Packet{ID: 1}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	want := []string{
		"enter first", "enter second", "enter third",
		"leave third", "leave second", "leave first",
	}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for index, step := range want {
		if order[index] != step {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestReceiveMiddlewareNestsInDeclarationOrder(t *testing.T) {
	t.Parallel()

	var order []string
	base := &recordingHandler{}

	chain, err := middleware.ChainReceive(
		base,
		noteReceive(&order, "first"),
		noteReceive(&order, "second"),
	)
	if err != nil {
		t.Fatalf("ChainReceive: %v", err)
	}
	if err := chain.Handle(t.Context(), protocol.Packet{ID: 1}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if len(order) != 4 || order[0] != "enter first" || order[3] != "leave first" {
		t.Fatalf("order = %v, want first outermost", order)
	}
}

func TestAnEmptyChainReturnsTheBaseUnchanged(t *testing.T) {
	t.Parallel()

	base := &recordingSender{}
	chain, err := middleware.ChainSend(base)
	if err != nil {
		t.Fatalf("ChainSend: %v", err)
	}
	if chain != middleware.Sender(base) {
		t.Fatal("an empty chain wrapped the base sender")
	}

	handler := &recordingHandler{}
	received, err := middleware.ChainReceive(handler)
	if err != nil {
		t.Fatalf("ChainReceive: %v", err)
	}
	if received != middleware.Handler(handler) {
		t.Fatal("an empty chain wrapped the base handler")
	}
}

func TestAMiddlewareErrorShortCircuitsTheChain(t *testing.T) {
	t.Parallel()

	refusal := errors.New("refused")
	base := &recordingSender{}

	chain, err := middleware.ChainSend(base, func(middleware.Sender) middleware.Sender {
		return middleware.SenderFunc(func(context.Context, protocol.Packet) error {
			return refusal
		})
	})
	if err != nil {
		t.Fatalf("ChainSend: %v", err)
	}

	if err := chain.Send(t.Context(), protocol.Packet{ID: 1}); !errors.Is(err, refusal) {
		t.Fatalf("Send error = %v, want the middleware's refusal", err)
	}
	if len(base.packets) != 0 {
		t.Fatal("the base sender ran after a middleware refused")
	}
}

func TestAMiddlewareCannotMutateTheCallersPayload(t *testing.T) {
	t.Parallel()

	base := &recordingSender{}
	chain, err := middleware.ChainSend(base, func(next middleware.Sender) middleware.Sender {
		return middleware.SenderFunc(func(ctx context.Context, packet protocol.Packet) error {
			packet.Payload[0] = 0xff

			return next.Send(ctx, packet)
		})
	})
	if err != nil {
		t.Fatalf("ChainSend: %v", err)
	}

	payload := []byte{0x01, 0x02}
	if err := chain.Send(t.Context(), protocol.Packet{ID: 1, Payload: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	if !bytes.Equal(payload, []byte{0x01, 0x02}) {
		t.Fatalf("the caller's payload is %x, want it untouched", payload)
	}
	if len(base.packets) != 1 || base.packets[0].Payload[0] != 0xff {
		t.Fatal("the middleware's edit did not reach the base sender")
	}
}

func TestANilMiddlewareIsAConstructionError(t *testing.T) {
	t.Parallel()

	if _, err := middleware.ChainSend(&recordingSender{}, nil); !errors.Is(err, middleware.ErrInvalidChain) {
		t.Fatalf("ChainSend error = %v, want ErrInvalidChain", err)
	}
	if _, err := middleware.ChainReceive(&recordingHandler{}, nil); !errors.Is(err, middleware.ErrInvalidChain) {
		t.Fatalf("ChainReceive error = %v, want ErrInvalidChain", err)
	}
}

func TestANilBaseIsAConstructionError(t *testing.T) {
	t.Parallel()

	if _, err := middleware.ChainSend(nil); !errors.Is(err, middleware.ErrInvalidChain) {
		t.Fatalf("ChainSend error = %v, want ErrInvalidChain", err)
	}
	if _, err := middleware.ChainReceive(nil); !errors.Is(err, middleware.ErrInvalidChain) {
		t.Fatalf("ChainReceive error = %v, want ErrInvalidChain", err)
	}
}

// TestAMiddlewareThatReturnsNilIsRejected covers the other way a chain can be
// built wrong: the middleware is present but hands back nothing to call.
func TestAMiddlewareThatReturnsNilIsRejected(t *testing.T) {
	t.Parallel()

	_, err := middleware.ChainSend(&recordingSender{}, func(middleware.Sender) middleware.Sender {
		return nil
	})
	if !errors.Is(err, middleware.ErrInvalidChain) {
		t.Fatalf("ChainSend error = %v, want ErrInvalidChain", err)
	}
}
