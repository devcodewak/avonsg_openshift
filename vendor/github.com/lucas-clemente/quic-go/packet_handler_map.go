package quic

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/lucas-clemente/quic-go/internal/protocol"
	"github.com/lucas-clemente/quic-go/internal/utils"
	"github.com/lucas-clemente/quic-go/internal/wire"
)

type packetHandlerEntry struct {
	handler    packetHandler
	resetToken *[16]byte
}

// The packetHandlerMap stores packetHandlers, identified by connection ID.
// It is used:
// * by the server to store sessions
// * when multiplexing outgoing connections to store clients
type packetHandlerMap struct {
	mutex sync.RWMutex

	conn      net.PacketConn
	connIDLen int

	handlers    map[string] /* string(ConnectionID)*/ packetHandlerEntry
	resetTokens map[[16]byte] /* stateless reset token */ packetHandler
	server      unknownPacketHandler
	closed      bool

	deleteRetiredSessionsAfter time.Duration

	logger utils.Logger
}

var _ packetHandlerManager = &packetHandlerMap{}

func newPacketHandlerMap(conn net.PacketConn, connIDLen int, logger utils.Logger) packetHandlerManager {
	m := &packetHandlerMap{
		conn:                       conn,
		connIDLen:                  connIDLen,
		handlers:                   make(map[string]packetHandlerEntry),
		resetTokens:                make(map[[16]byte]packetHandler),
		deleteRetiredSessionsAfter: protocol.RetiredConnectionIDDeleteTimeout,
		logger:                     logger,
	}
	go m.listen()
	return m
}

func (h *packetHandlerMap) Add(id protocol.ConnectionID, handler packetHandler) {
	h.mutex.Lock()
	h.handlers[string(id)] = packetHandlerEntry{handler: handler}
	h.mutex.Unlock()
}

func (h *packetHandlerMap) AddWithResetToken(id protocol.ConnectionID, handler packetHandler, token [16]byte) {
	h.mutex.Lock()
	h.handlers[string(id)] = packetHandlerEntry{handler: handler, resetToken: &token}
	h.resetTokens[token] = handler
	h.mutex.Unlock()
}

func (h *packetHandlerMap) Remove(id protocol.ConnectionID) {
	h.removeByConnectionIDAsString(string(id))
}

func (h *packetHandlerMap) removeByConnectionIDAsString(id string) {
	h.mutex.Lock()
	if handlerEntry, ok := h.handlers[id]; ok {
		if token := handlerEntry.resetToken; token != nil {
			delete(h.resetTokens, *token)
		}
		delete(h.handlers, id)
	}
	h.mutex.Unlock()
}

func (h *packetHandlerMap) Retire(id protocol.ConnectionID) {
	h.retireByConnectionIDAsString(string(id))
}

func (h *packetHandlerMap) retireByConnectionIDAsString(id string) {
	time.AfterFunc(h.deleteRetiredSessionsAfter, func() {
		h.removeByConnectionIDAsString(id)
	})
}

func (h *packetHandlerMap) SetServer(s unknownPacketHandler) {
	h.mutex.Lock()
	h.server = s
	h.mutex.Unlock()
}

func (h *packetHandlerMap) CloseServer() {
	h.mutex.Lock()
	h.server = nil
	var wg sync.WaitGroup
	for id, handlerEntry := range h.handlers {
		handler := handlerEntry.handler
		if handler.GetPerspective() == protocol.PerspectiveServer {
			wg.Add(1)
			go func(id string, handler packetHandler) {
				// session.Close() blocks until the CONNECTION_CLOSE has been sent and the run-loop has stopped
				_ = handler.Close()
				h.retireByConnectionIDAsString(id)
				wg.Done()
			}(id, handler)
		}
	}
	h.mutex.Unlock()
	wg.Wait()
}

func (h *packetHandlerMap) close(e error) error {
	h.mutex.Lock()
	if h.closed {
		h.mutex.Unlock()
		return nil
	}
	h.closed = true

	var wg sync.WaitGroup
	for _, handlerEntry := range h.handlers {
		wg.Add(1)
		go func(handlerEntry packetHandlerEntry) {
			handlerEntry.handler.destroy(e)
			wg.Done()
		}(handlerEntry)
	}

	if h.server != nil {
		h.server.closeWithError(e)
	}
	h.mutex.Unlock()
	wg.Wait()
	return nil
}

func (h *packetHandlerMap) listen() {
	for {
		data := *getPacketBuffer()
		data = data[:protocol.MaxReceivePacketSize]
		// The packet size should not exceed protocol.MaxReceivePacketSize bytes
		// If it does, we only read a truncated packet, which will then end up undecryptable
		n, addr, err := h.conn.ReadFrom(data)
		if err != nil {
			h.close(err)
			return
		}
		data = data[:n]

		if err := h.handlePacket(addr, data); err != nil {
			h.logger.Debugf("error handling packet from %s: %s", addr, err)
		}
	}
}

func (h *packetHandlerMap) handlePacket(addr net.Addr, data []byte) error {
	rcvTime := time.Now()

	r := bytes.NewReader(data)
	iHdr, err := wire.ParseInvariantHeader(r, h.connIDLen)
	// drop the packet if we can't parse the header
	if err != nil {
		return fmt.Errorf("error parsing invariant header: %s", err)
	}

	h.mutex.RLock()
	handlerEntry, handlerFound := h.handlers[string(iHdr.DestConnectionID)]
	server := h.server

	var sentBy protocol.Perspective
	var version protocol.VersionNumber
	var handlePacket func(*receivedPacket)
	if handlerFound { // existing session
		handler := handlerEntry.handler
		sentBy = handler.GetPerspective().Opposite()
		version = handler.GetVersion()
		handlePacket = handler.handlePacket
	} else { // no session found
		// this might be a stateless reset
		if !iHdr.IsLongHeader {
			if len(data) >= protocol.MinStatelessResetSize {
				var token [16]byte
				copy(token[:], data[len(data)-16:])
				if sess, ok := h.resetTokens[token]; ok {
					h.mutex.RUnlock()
					sess.destroy(errors.New("received a stateless reset"))
					return nil
				}
			}
			// TODO(#943): send a stateless reset
			return fmt.Errorf("received a short header packet with an unexpected connection ID %s", iHdr.DestConnectionID)
		}
		if server == nil { // no server set
			h.mutex.RUnlock()
			return fmt.Errorf("received a packet with an unexpected connection ID %s", iHdr.DestConnectionID)
		}
		handlePacket = server.handlePacket
		sentBy = protocol.PerspectiveClient
		version = iHdr.Version
	}
	h.mutex.RUnlock()

	hdr, err := iHdr.Parse(r, sentBy, version)
	if err != nil {
		return fmt.Errorf("error parsing header: %s", err)
	}
	hdr.Raw = data[:len(data)-r.Len()]
	packetData := data[len(data)-r.Len():]

	if hdr.IsLongHeader {
		if hdr.Length < protocol.ByteCount(hdr.PacketNumberLen) {
			return fmt.Errorf("packet length (%d bytes) shorter than packet number (%d bytes)", hdr.Length, hdr.PacketNumberLen)
		}
		if protocol.ByteCount(len(packetData))+protocol.ByteCount(hdr.PacketNumberLen) < hdr.Length {
			return fmt.Errorf("packet length (%d bytes) is smaller than the expected length (%d bytes)", len(packetData)+int(hdr.PacketNumberLen), hdr.Length)
		}
		packetData = packetData[:int(hdr.Length)-int(hdr.PacketNumberLen)]
		// TODO(#1312): implement parsing of compound packets
	}

	handlePacket(&receivedPacket{
		remoteAddr: addr,
		header:     hdr,
		data:       packetData,
		rcvTime:    rcvTime,
	})
	return nil
}
