package container

import (
	"bytes"
	"encoding/gob"
	"fmt"

	"github.com/tobiichi3227/go-sandbox/pkg/unixsocket"
)

// 16k buffer size
const bufferSize = 16 << 10

type socket struct {
	*unixsocket.Socket

	buff []byte

	decoder  *gob.Decoder
	recvBuff bufferRotator

	encoder  *gob.Encoder
	sendBuff bytes.Buffer
}

// bufferRotator replace the underlying Buffers to avoid allocation
type bufferRotator struct {
	*bytes.Buffer
}

func (b *bufferRotator) Rotate(buffer *bytes.Buffer) {
	b.Buffer = buffer
}

func newSocket(s *unixsocket.Socket) *socket {
	soc := socket{
		Socket: s,
	}
	soc.buff = make([]byte, bufferSize)
	soc.decoder = gob.NewDecoder(&soc.recvBuff)
	soc.encoder = gob.NewEncoder(&soc.sendBuff)

	return &soc
}

func (s *socket) RecvMsg(e any) (msg unixsocket.Msg, err error) {
	n, msg, err := s.Socket.RecvMsg(s.buff)
	if err != nil {
		return msg, fmt.Errorf("recv msg: %w", err)
	}
	s.recvBuff.Rotate(bytes.NewBuffer(s.buff[:n]))

	if err := s.decoder.Decode(e); err != nil {
		return msg, fmt.Errorf("recv msg: decode: %w", err)
	}
	return msg, nil
}

func (s *socket) SendMsg(e any, msg unixsocket.Msg) error {
	s.sendBuff.Reset()
	if err := s.encoder.Encode(e); err != nil {
		return fmt.Errorf("send msg: encode: %w", err)
	}

	if err := s.Socket.SendMsg(s.sendBuff.Bytes(), msg); err != nil {
		return fmt.Errorf("send msg: %w", err)
	}
	return nil
}
