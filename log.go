package main

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
)

// rawMsg is the part of a log record that the index needs. The envelope
// fields stay raw too: one field of an odd type must not drop the whole
// record, because the record still belongs to the author's feed.
type rawMsg struct {
	Key      string          `json:"key"`
	Received json.RawMessage `json:"timestamp"` // when the pub got it
	Value    struct {
		Author    string          `json:"author"`
		Sequence  json.RawMessage `json:"sequence"`
		Timestamp json.RawMessage `json:"timestamp"` // what the author's client claims
		Content   json.RawMessage `json:"content"`
	} `json:"value"`
}

// num reads a JSON number, or 0.
func num(raw json.RawMessage) float64 {
	var f float64
	json.Unmarshal(raw, &f)
	return f
}

// flumelog is a flumelog-offset file: each record is
// length (uint32 BE), data, length, end offset (uint32 BE).
type flumelog struct {
	f    *os.File
	size int64
}

func openLog(path string) (*flumelog, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &flumelog{f, st.Size()}, nil
}

func (l *flumelog) Close() error { return l.f.Close() }

// scan calls fn for each record in log order, with the record's bytes. A
// truncated or corrupt tail (a length that runs past the end of the file)
// ends the scan without an error, because a writer that stopped mid-record
// leaves one. A length is checked before anything is allocated for it.
func (l *flumelog) scan(fn func(off int64, m *rawMsg, data []byte)) error {
	r := bufio.NewReaderSize(io.NewSectionReader(l.f, 0, l.size), 1<<20)
	var off int64
	var hdr [4]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil
			}
			return err
		}
		n := int64(binary.BigEndian.Uint32(hdr[:]))
		if off+4+n+8 > l.size {
			return nil
		}
		buf := make([]byte, n)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
		if _, err := r.Discard(8); err != nil {
			return err
		}
		var m rawMsg
		if n > 0 && json.Unmarshal(buf, &m) == nil {
			fn(off, &m, buf)
		}
		off += 4 + n + 8
	}
}

// readAt returns the data of the record that starts at off.
func (l *flumelog) readAt(off int64) ([]byte, error) {
	var hdr [4]byte
	if _, err := l.f.ReadAt(hdr[:], off); err != nil {
		return nil, err
	}
	n := int64(binary.BigEndian.Uint32(hdr[:]))
	if off+4+n > l.size {
		return nil, errors.New("record runs past the end of the log")
	}
	buf := make([]byte, n)
	if _, err := l.f.ReadAt(buf, off+4); err != nil {
		return nil, err
	}
	return buf, nil
}
