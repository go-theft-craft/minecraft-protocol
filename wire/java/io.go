package java

import "io"

func writeFull(w io.Writer, data []byte) (int, error) {
	written := 0
	for written < len(data) {
		n, err := w.Write(data[written:])
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}
