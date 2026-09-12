package atualizador

import "testing"

func TestMaisNova(t *testing.T) {
	casos := []struct {
		remota, local string
		querIr        bool
	}{
		{"v2.0.0", "1.2.2", true},
		{"2.0.1", "2.0.0", true},
		{"2.0.0", "2.0.0", false},
		{"1.9.9", "2.0.0", false},
		{"v2.1", "2.0.9", true},
		{"2.0.0-rc1", "2.0.0", false}, // só os dígitos contam
		{"2.0.2", "v2.0.10", false},   // compara número, não texto
	}
	for _, c := range casos {
		if got := MaisNova(c.remota, c.local); got != c.querIr {
			t.Errorf("MaisNova(%q, %q) = %v, queria %v", c.remota, c.local, got, c.querIr)
		}
	}
}
