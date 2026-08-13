package java

import (
	"bytes"
	"fmt"
	"io"
	"reflect"

	"github.com/go-theft-craft/minecraft-protocol"
)

const tagName = "mc"

type decodedField struct {
	target reflect.Value
	value  reflect.Value
}

type payloadWriter struct {
	writer    io.Writer
	remaining int
	limit     int
}

func (w *payloadWriter) Write(data []byte) (int, error) {
	if len(data) > w.remaining {
		return 0, fmt.Errorf("packet payload exceeds limit %d: %w", w.limit, ErrFrameTooLarge)
	}

	written, err := w.writer.Write(data)
	w.remaining -= written
	return written, err
}

// Marshal encodes a packet value using its mc struct tags.
func Marshal(value PacketValue, limits protocol.Limits) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}

	reflected, err := marshalValue(value)
	if err != nil {
		return nil, err
	}
	if err := validateRestOrder(reflected.Type()); err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	payloadLimit := reflectedPayloadLimit(value, limits)

	var encoded bytes.Buffer
	bounded := &payloadWriter{writer: &encoded, remaining: payloadLimit, limit: payloadLimit}
	typeOfValue := reflected.Type()
	for index := range typeOfValue.NumField() {
		field := typeOfValue.Field(index)
		tag := field.Tag.Get(tagName)
		if tag == "" || tag == "-" {
			continue
		}

		fieldValue := reflected.Field(index)
		if !fieldValue.CanInterface() {
			return nil, fmt.Errorf("marshal field %s tag %q: field cannot be accessed", field.Name, tag)
		}
		if err := writeField(bounded, limits, tag, fieldValue.Interface()); err != nil {
			return nil, fmt.Errorf("marshal field %s: %w", field.Name, err)
		}
	}

	return encoded.Bytes(), nil
}

// Unmarshal decodes bytes into a packet value using its mc struct tags.
func Unmarshal(data []byte, value PacketValue, limits protocol.Limits) error {
	if err := validateLimits(limits); err != nil {
		return err
	}

	reflected, err := unmarshalValue(value)
	if err != nil {
		return err
	}
	if err := validateRestOrder(reflected.Type()); err != nil {
		return fmt.Errorf("unmarshal: %w", err)
	}
	payloadLimit := reflectedPayloadLimit(value, limits)
	if len(data) > payloadLimit {
		return fmt.Errorf(
			"unmarshal packet payload size %d exceeds limit %d: %w",
			len(data),
			payloadLimit,
			ErrFrameTooLarge,
		)
	}

	reader := bytes.NewReader(data)
	typeOfValue := reflected.Type()
	decoded := make([]decodedField, 0, typeOfValue.NumField())
	for index := range typeOfValue.NumField() {
		field := typeOfValue.Field(index)
		tag := field.Tag.Get(tagName)
		if tag == "" || tag == "-" {
			continue
		}

		target := reflected.Field(index)
		if !target.CanSet() {
			return fmt.Errorf("unmarshal field %s tag %q: field cannot be set", field.Name, tag)
		}

		value, err := readField(reader, limits, tag)
		if err != nil {
			return fmt.Errorf("unmarshal field %s: %w", field.Name, err)
		}
		decodedValue := reflect.ValueOf(value)
		if !decodedValue.IsValid() || !decodedValue.Type().AssignableTo(target.Type()) {
			return fmt.Errorf(
				"unmarshal field %s tag %q: expected %s, got %s",
				field.Name,
				tag,
				target.Type(),
				reflectedType(decodedValue),
			)
		}
		decoded = append(decoded, decodedField{target: target, value: decodedValue})
	}

	for _, field := range decoded {
		field.target.Set(field.value)
	}
	return nil
}

// ReadPacket reads one framed packet and decodes its payload into value.
func ReadPacket(r io.Reader, limits protocol.Limits, value PacketValue) error {
	if err := validateLimits(limits); err != nil {
		return err
	}
	reflected, err := unmarshalValue(value)
	if err != nil {
		return err
	}
	if err := validateRestOrder(reflected.Type()); err != nil {
		return fmt.Errorf("read packet: %w", err)
	}

	expectedID := value.PacketID()
	packet, err := ReadRawPacket(r, limits)
	if err != nil {
		return err
	}
	if packet.ID != expectedID {
		return fmt.Errorf("expected packet 0x%02X, got 0x%02X", expectedID, packet.ID)
	}
	if err := Unmarshal(packet.Payload, value, limits); err != nil {
		return fmt.Errorf("unmarshal packet 0x%02X: %w", expectedID, err)
	}
	return nil
}

// WritePacket encodes value and writes one framed packet.
func WritePacket(w io.Writer, limits protocol.Limits, value PacketValue) error {
	data, err := Marshal(value, limits)
	if err != nil {
		return fmt.Errorf("marshal packet: %w", err)
	}

	packet := protocol.Packet{ID: value.PacketID(), Payload: data}
	if err := WriteRawPacket(w, limits, packet); err != nil {
		return fmt.Errorf("write packet 0x%02X: %w", packet.ID, err)
	}
	return nil
}

func marshalValue(value PacketValue) (reflect.Value, error) {
	if value == nil {
		return reflect.Value{}, fmt.Errorf("marshal: expected struct, got <nil>")
	}

	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Pointer {
		if reflected.IsNil() {
			return reflect.Value{}, fmt.Errorf("marshal: expected struct, got nil %s", reflected.Type())
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("marshal: expected struct, got %s", reflected.Kind())
	}
	return reflected, nil
}

func unmarshalValue(value PacketValue) (reflect.Value, error) {
	if value == nil {
		return reflect.Value{}, fmt.Errorf("unmarshal: expected non-nil pointer, got <nil>")
	}

	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Pointer || reflected.IsNil() {
		return reflect.Value{}, fmt.Errorf("unmarshal: expected non-nil pointer, got %T", value)
	}

	reflected = reflected.Elem()
	if reflected.Kind() != reflect.Struct {
		return reflect.Value{}, fmt.Errorf("unmarshal: expected pointer to struct, got pointer to %s", reflected.Kind())
	}
	return reflected, nil
}

func validateRestOrder(typeOfValue reflect.Type) error {
	for index := range typeOfValue.NumField() {
		field := typeOfValue.Field(index)
		if field.Tag.Get(tagName) != "rest" {
			continue
		}

		for laterIndex := index + 1; laterIndex < typeOfValue.NumField(); laterIndex++ {
			laterField := typeOfValue.Field(laterIndex)
			laterTag := laterField.Tag.Get(tagName)
			if laterTag == "" || laterTag == "-" {
				continue
			}
			return fmt.Errorf(
				"field %s tag %q must be last; field %s tag %q follows it",
				field.Name,
				"rest",
				laterField.Name,
				laterTag,
			)
		}
	}
	return nil
}

func reflectedType(value reflect.Value) string {
	if !value.IsValid() {
		return "<nil>"
	}
	return value.Type().String()
}

func reflectedPayloadLimit(value PacketValue, limits protocol.Limits) int {
	return limits.FrameBytes() - VarIntSize(value.PacketID())
}
