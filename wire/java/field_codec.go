package java

import (
	"fmt"
	"io"

	"github.com/go-theft-craft/minecraft-protocol"
)

func writeField(w io.Writer, limits protocol.Limits, tag string, value any) error {
	switch tag {
	case "varint":
		value, err := fieldValue[int32](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteVarInt(w, value)
		return err
	case "varlong":
		value, err := fieldValue[int64](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteVarLong(w, value)
		return err
	case "i8":
		value, err := fieldValue[int8](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteI8(w, value)
		return err
	case "u8":
		value, err := fieldValue[uint8](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteU8(w, value)
		return err
	case "i16":
		value, err := fieldValue[int16](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteI16(w, value)
		return err
	case "u16":
		value, err := fieldValue[uint16](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteU16(w, value)
		return err
	case "i32":
		value, err := fieldValue[int32](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteI32(w, value)
		return err
	case "i64", "position":
		value, err := fieldValue[int64](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteI64(w, value)
		return err
	case "f32":
		value, err := fieldValue[float32](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteF32(w, value)
		return err
	case "f64":
		value, err := fieldValue[float64](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteF64(w, value)
		return err
	case "bool":
		value, err := fieldValue[bool](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteBool(w, value)
		return err
	case "string":
		value, err := fieldValue[string](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteString(w, limits, value)
		return err
	case "uuid":
		value, err := fieldValue[[16]byte](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteUUID(w, value)
		return err
	case "bytearray":
		value, err := fieldValue[[]byte](tag, value)
		if err != nil {
			return err
		}
		_, err = WriteByteArray(w, limits, value)
		return err
	case "rest":
		value, err := fieldValue[[]byte](tag, value)
		if err != nil {
			return err
		}
		_, err = writeFull(w, value)
		return err
	default:
		return fmt.Errorf("tag %q: unknown field tag", tag)
	}
}

func readField(r io.Reader, limits protocol.Limits, tag string) (any, error) {
	switch tag {
	case "varint":
		value, _, err := ReadVarInt(r)
		return value, err
	case "varlong":
		value, _, err := ReadVarLong(r)
		return value, err
	case "i8":
		return ReadI8(r)
	case "u8":
		return ReadU8(r)
	case "i16":
		return ReadI16(r)
	case "u16":
		return ReadU16(r)
	case "i32":
		return ReadI32(r)
	case "i64", "position":
		return ReadI64(r)
	case "f32":
		return ReadF32(r)
	case "f64":
		return ReadF64(r)
	case "bool":
		return ReadBool(r)
	case "string":
		return ReadString(r, limits)
	case "uuid":
		return ReadUUID(r)
	case "bytearray":
		return ReadByteArray(r, limits)
	case "rest":
		return io.ReadAll(r)
	default:
		return nil, fmt.Errorf("tag %q: unknown field tag", tag)
	}
}

func fieldValue[T any](tag string, value any) (T, error) {
	typed, ok := value.(T)
	if ok {
		return typed, nil
	}

	var zero T
	return zero, fmt.Errorf("tag %q: expected %T, got %T", tag, zero, value)
}
