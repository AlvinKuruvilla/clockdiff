package runtime

import (
	"reflect"
	"testing"

	"github.com/moby/moby/api/types/network"
)

func portSet(t *testing.T, specs ...string) network.PortSet {
	t.Helper()

	set := make(network.PortSet, len(specs))
	for _, spec := range specs {
		port, err := network.ParsePort(spec)
		if err != nil {
			t.Fatalf("ParsePort(%q): %v", spec, err)
		}
		set[port] = struct{}{}
	}
	return set
}

func TestDeclaredPorts(t *testing.T) {
	tests := []struct {
		name    string
		exposed []string
		want    map[uint16]bool
	}{
		{"nothing exposed", nil, map[uint16]bool{}},
		{"one tcp port", []string{"8000/tcp"}, map[uint16]bool{8000: true}},
		{
			"several",
			[]string{"80/tcp", "443/tcp"},
			map[uint16]bool{80: true, 443: true},
		},
		{
			// A socket table read over /proc/net/tcp can never show a UDP
			// listener, so keeping one would make the service look like it
			// never became ready.
			"udp is dropped",
			[]string{"53/udp"},
			map[uint16]bool{},
		},
		{
			"same number on both protocols keeps only tcp",
			[]string{"53/tcp", "53/udp"},
			map[uint16]bool{53: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := declaredPorts(portSet(t, tt.exposed...))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("declaredPorts(%v) = %v, want %v", tt.exposed, got, tt.want)
			}
		})
	}
}
