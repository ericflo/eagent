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
	// atEnd: the last call read up to the end the file had then.
	atEnd bool
}

// next returns the complete lines appended since the last call, and the
// offset in the file where each one starts.
func (t *tailer) next() ([][]byte, []int64, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if st.Size() < t.offset {
		// Truncated or replaced: start again from the top.
		t.offset, t.rest = 0, nil
	}
	t.atEnd = false
	if st.Size() == t.offset {
		t.atEnd = true
		return nil, nil, nil
	}
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil, nil, err
	}
	// Bound one read so a huge backlog is consumed over several polls.
	const chunk = 8 << 20
	buf := make([]byte, min(st.Size()-t.offset, chunk))
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF {
		return nil, nil, err
	}
	at := t.offset - int64(len(t.rest)) // where data begins in the file
	t.offset += int64(n)
	t.atEnd = t.offset >= st.Size()
	data := append(t.rest, buf[:n]...)
	if t.skipPartial {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			t.rest = nil
			return nil, nil, nil
		}
		data, t.skipPartial, at = data[i+1:], false, at+int64(i+1)
	}
	var lines [][]byte
	var starts []int64
	for {
		i := bytes.IndexByte(data, '\n')
		if i < 0 {
			break
		}
		if line := bytes.TrimSpace(data[:i]); len(line) > 0 {
			lines = append(lines, append([]byte(nil), line...))
			starts = append(starts, at)
		}
		data, at = data[i+1:], at+int64(i+1)
	}
	t.rest = append([]byte(nil), data...)
	return lines, starts, nil
}
