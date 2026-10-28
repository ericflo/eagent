package relay

import (
	"bytes"
	"io"
	"os"
)

// tailer follows a JSONL file that another program appends to, returning
// complete lines only. A partial last line is kept until its newline lands.
type tailer struct {
	path   string
	offset int64
	rest   []byte
	// skipPartial drops the bytes up to the first newline: the read started
	// mid-file, inside a line.
	skipPartial bool
}

// next returns the complete lines appended since the last call.
func (t *tailer) next() ([][]byte, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() < t.offset {
		// Truncated or replaced: start again from the top.
		t.offset, t.rest = 0, nil
	}
	if st.Size() == t.offset {
		return nil, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, err
	}
	// Bound one read so a huge backlog is consumed over several polls.
	const chunk = 8 << 20
	buf := make([]byte, min(st.Size()-t.offset, chunk))
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	t.offset += int64(n)
	data := append(t.rest, buf[:n]...)
	if t.skipPartial {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			t.rest = nil
			return nil, nil
		}
		data, t.skipPartial = data[i+1:], false
	}
	var lines [][]byte
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if line := bytes.TrimSpace(data[:i]); len(line) > 0 {
			lines = append(lines, append([]byte(nil), line...))
		}
		data = data[i+1:]
	}
	t.rest = append([]byte(nil), data...)
	return lines, nil
}
