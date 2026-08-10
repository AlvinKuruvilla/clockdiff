package runtime

import (
	"strings"
	"testing"
)

// Captured from a real container listening on 0.0.0.0:8000, 127.0.0.1:8001,
// :::8002 and ::1:8003, with both files concatenated as `cat` returns them.
const procNetTCPSample = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F40 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 325981 1 00000000491a17f0 100 0 0 10 0
   1: 0100007F:1F41 00000000:0000 0A 00000000:00000003 00:00000000 00000000     0        0 325982 1 000000002f49b2e9 100 0 0 10 0
  sl  local_address                         remote_address                        st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000000000000000000000000000:1F42 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 325983 1 00000000707b3d5a 100 0 0 10 0
   1: 00000000000000000000000001000000:1F43 00000000000000000000000000000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 325984 1 00000000f239af35 100 0 0 10 0
`

func TestParseProcNetTCP(t *testing.T) {
	sockets, err := ParseProcNetTCP(strings.NewReader(procNetTCPSample))
	if err != nil {
		t.Fatalf("ParseProcNetTCP: %v", err)
	}
	if len(sockets) != 4 {
		t.Fatalf("parsed %d sockets, want 4", len(sockets))
	}

	// A header appears mid-stream because both files are concatenated, and
	// tcp6 rows must survive the wider address column.
	want := []uint16{8000, 8001, 8002, 8003}
	for i, port := range want {
		if sockets[i].LocalPort != port {
			t.Errorf("socket %d port = %d, want %d", i, sockets[i].LocalPort, port)
		}
		if !sockets[i].Listening() {
			t.Errorf("socket %d state = %v, want LISTEN", i, sockets[i].State)
		}
	}

	if got := sockets[1].AcceptQueue; got != 3 {
		t.Errorf("accept queue = %d, want 3", got)
	}
	if got := sockets[3].LocalAddress; got != "00000000000000000000000001000000" {
		t.Errorf("tcp6 address = %q, want the full 32-character form", got)
	}
}

// Port 80 is written 0050. Parsed as decimal it comes back as fifty rather
// than failing, so a base error here is silent.
func TestPortsAreHex(t *testing.T) {
	line := `   0: 00000000:0050 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1234 1 0 100 0 0 10 0`

	sockets, err := ParseProcNetTCP(strings.NewReader(line))
	if err != nil {
		t.Fatalf("ParseProcNetTCP: %v", err)
	}
	if len(sockets) != 1 {
		t.Fatalf("parsed %d sockets, want 1", len(sockets))
	}
	if sockets[0].LocalPort != 80 {
		t.Errorf("port = %d, want 80", sockets[0].LocalPort)
	}
}

// The state column is a hex byte, not a name.
func TestNonListeningStatesAreParsed(t *testing.T) {
	line := `   0: 0100007F:1F90 0100007F:8AE2 01 00000000:00000000 00:00000000 00000000  1000        0 4321 1 0 100 0 0 10 0`

	sockets, err := ParseProcNetTCP(strings.NewReader(line))
	if err != nil {
		t.Fatalf("ParseProcNetTCP: %v", err)
	}
	if sockets[0].Listening() {
		t.Error("an ESTABLISHED socket reported as listening")
	}
	if got := sockets[0].State.String(); got != "ESTABLISHED" {
		t.Errorf("state = %s, want ESTABLISHED", got)
	}
	if got := sockets[0].UID; got != 1000 {
		t.Errorf("uid = %d, want 1000 (decimal, unlike everything else)", got)
	}
}

// The kernel formats this file while walking a live socket table, so a row can
// be torn. One bad row must not lose the rest of the table.
func TestMalformedRowsAreSkipped(t *testing.T) {
	sample := `   0: 00000000:1F40 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 1 1 0 100 0 0 10 0
   1: truncated
   2: 00000000:1F41 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1 0 100 0 0 10 0`

	sockets, err := ParseProcNetTCP(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("ParseProcNetTCP: %v", err)
	}
	if len(sockets) != 2 {
		t.Fatalf("parsed %d sockets, want the 2 intact rows", len(sockets))
	}
}
