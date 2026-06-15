package cast

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/ygelfand/lib-yttv/cast/castpb"
	"google.golang.org/protobuf/proto"
)

func writeFrame(w io.Writer, m *castpb.CastMessage) error {
	data, err := proto.Marshal(m)
	if err != nil {
		return err
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(data)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

func readFrame(r io.Reader) (*castpb.CastMessage, error) {
	var lenBuf [4]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(lenBuf[:])
	if n == 0 || n > 1<<20 {
		return nil, errors.New("cast: frame size out of range")
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	var m castpb.CastMessage
	if err := proto.Unmarshal(buf, &m); err != nil {
		return nil, err
	}
	return &m, nil
}
