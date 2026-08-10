package runtime

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// tcpState is the connection state as /proc/net/tcp records it: a hex byte,
// not a name.
type tcpState uint8

const (
	tcpEstablished tcpState = 0x01
	tcpSynSent     tcpState = 0x02
	tcpSynRecv     tcpState = 0x03
	tcpFinWait1    tcpState = 0x04
	tcpFinWait2    tcpState = 0x05
	tcpTimeWait    tcpState = 0x06
	tcpClose       tcpState = 0x07
	tcpCloseWait   tcpState = 0x08
	tcpLastAck     tcpState = 0x09
	tcpListen      tcpState = 0x0A
	tcpClosing     tcpState = 0x0B
	tcpNewSynRecv  tcpState = 0x0C
)

func (s tcpState) String() string {
	switch s {
	case tcpEstablished:
		return "ESTABLISHED"
	case tcpSynSent:
		return "SYN_SENT"
	case tcpSynRecv:
		return "SYN_RECV"
	case tcpFinWait1:
		return "FIN_WAIT1"
	case tcpFinWait2:
		return "FIN_WAIT2"
	case tcpTimeWait:
		return "TIME_WAIT"
	case tcpClose:
		return "CLOSE"
	case tcpCloseWait:
		return "CLOSE_WAIT"
	case tcpLastAck:
		return "LAST_ACK"
	case tcpListen:
		return "LISTEN"
	case tcpClosing:
		return "CLOSING"
	case tcpNewSynRecv:
		return "NEW_SYN_RECV"
	default:
		return fmt.Sprintf("UNKNOWN(0x%02X)", uint8(s))
	}
}

// TCPSocket is one row of /proc/net/tcp or /proc/net/tcp6.
//
// The local and remote addresses are kept as the raw hex the kernel prints.
// Decoding them is per-4-byte-word little-endian — 127.0.0.1 appears as
// 0100007F — and nothing here needs it: readiness is a question about ports
// and states, so the only reason to decode would be telling a loopback-only
// bind from a real one.
type TCPSocket struct {
	LocalAddress  string
	LocalPort     uint16
	RemoteAddress string
	RemotePort    uint16
	State         tcpState

	// AcceptQueue is how many completed connections are waiting for the
	// process to call accept(). Non-zero on a listening socket means the
	// process is bound but not yet servicing connections.
	AcceptQueue uint64

	UID   uint32
	Inode uint64
}

// Listening reports whether this socket is accepting new connections.
func (s TCPSocket) Listening() bool { return s.State == tcpListen }

// ParseProcNetTCP reads the concatenation of /proc/net/tcp and /proc/net/tcp6.
//
// Both files have the same column layout; only the address width differs, and
// since nothing here decodes addresses one parser covers both. That matters
// because a socket bound to :: appears solely in tcp6 while still serving
// IPv4, which is what most frameworks do when told to listen on all
// interfaces.
//
// Malformed rows are skipped rather than failing the read. The kernel formats
// this file by walking a live socket table, so a row can be torn by concurrent
// churn; a caller polling on a ticker sees the socket on the next pass.
func ParseProcNetTCP(r io.Reader) ([]TCPSocket, error) {
	var sockets []TCPSocket

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Concatenating both files puts a header in the middle of the stream,
		// so headers are recognised rather than counted.
		if line == "" || strings.HasPrefix(line, "sl") {
			continue
		}

		socket, err := parseSocketLine(line)
		if err != nil {
			continue
		}
		sockets = append(sockets, socket)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read /proc/net/tcp: %w", err)
	}
	return sockets, nil
}

// Column positions in a /proc/net/tcp row. The header names more columns than
// the rows contain — tx_queue and rx_queue are printed as one colon-joined
// field — so these are indices into the data, not into the header.
const (
	fieldLocal   = 1
	fieldRemote  = 2
	fieldState   = 3
	fieldQueues  = 4
	fieldUID     = 7
	fieldInode   = 9
	fieldMinimum = fieldInode + 1
)

func parseSocketLine(line string) (TCPSocket, error) {
	fields := strings.Fields(line)
	if len(fields) < fieldMinimum {
		return TCPSocket{}, fmt.Errorf("expected at least %d columns, got %d", fieldMinimum, len(fields))
	}

	localAddr, localPort, err := splitHexAddress(fields[fieldLocal])
	if err != nil {
		return TCPSocket{}, fmt.Errorf("local address: %w", err)
	}
	remoteAddr, remotePort, err := splitHexAddress(fields[fieldRemote])
	if err != nil {
		return TCPSocket{}, fmt.Errorf("remote address: %w", err)
	}

	// Everything the kernel prints here is hex except uid and inode. Parsing a
	// port as decimal does not merely fail: port 80 is written 0050 and would
	// come back as fifty.
	state, err := strconv.ParseUint(fields[fieldState], 16, 8)
	if err != nil {
		return TCPSocket{}, fmt.Errorf("state %q: %w", fields[fieldState], err)
	}

	_, rxQueue, found := strings.Cut(fields[fieldQueues], ":")
	if !found {
		return TCPSocket{}, fmt.Errorf("queues %q: expected tx:rx", fields[fieldQueues])
	}
	acceptQueue, err := strconv.ParseUint(rxQueue, 16, 64)
	if err != nil {
		return TCPSocket{}, fmt.Errorf("accept queue %q: %w", rxQueue, err)
	}

	uid, err := strconv.ParseUint(fields[fieldUID], 10, 32)
	if err != nil {
		return TCPSocket{}, fmt.Errorf("uid %q: %w", fields[fieldUID], err)
	}
	inode, err := strconv.ParseUint(fields[fieldInode], 10, 64)
	if err != nil {
		return TCPSocket{}, fmt.Errorf("inode %q: %w", fields[fieldInode], err)
	}

	return TCPSocket{
		LocalAddress:  localAddr,
		LocalPort:     localPort,
		RemoteAddress: remoteAddr,
		RemotePort:    remotePort,
		State:         tcpState(state),
		AcceptQueue:   acceptQueue,
		UID:           uint32(uid),
		Inode:         inode,
	}, nil
}

// splitHexAddress splits ADDRESS:PORT, where both halves are hex and the
// address is 8 characters for IPv4 or 32 for IPv6.
func splitHexAddress(field string) (address string, port uint16, err error) {
	address, portHex, found := strings.Cut(field, ":")
	if !found {
		return "", 0, fmt.Errorf("%q: expected address:port", field)
	}

	parsed, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil {
		return "", 0, fmt.Errorf("port %q: %w", portHex, err)
	}
	return address, uint16(parsed), nil
}
