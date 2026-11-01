package sandbox

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	wasmPageSize     = 64 << 10
	memorySectionID  = 5
	limitsMinOnly    = 0x00
	limitsMinAndMax  = 0x01
	wasmHeaderLength = 8
)

// withMemoryMax returns a copy of a wasm binary whose single linear memory
// declares maxPages as its maximum. The shipped qjs.wasm declares no maximum,
// so memory.grow only fails at 4 GiB; with a maximum, wazero refuses growth
// past it, malloc returns NULL, and QuickJS raises "out of memory".
func withMemoryMax(wasm []byte, maxPages uint32) ([]byte, error) {
	if len(wasm) < wasmHeaderLength {
		return nil, errors.New("wasm too short")
	}
	out := append([]byte(nil), wasm[:wasmHeaderLength]...)
	for pos := wasmHeaderLength; pos < len(wasm); {
		id := wasm[pos]
		size, n := binary.Uvarint(wasm[pos+1:])
		if n <= 0 {
			return nil, fmt.Errorf("bad section size at %d", pos)
		}
		start := pos + 1 + n
		end := start + int(size)
		if end > len(wasm) {
			return nil, fmt.Errorf("section at %d overruns the module", pos)
		}
		payload := wasm[start:end]
		if id == memorySectionID {
			var err error
			if payload, err = boundMemoryPayload(payload, maxPages); err != nil {
				return nil, err
			}
		}
		out = append(out, id)
		out = binary.AppendUvarint(out, uint64(len(payload)))
		out = append(out, payload...)
		pos = end
	}
	return out, nil
}

// boundMemoryPayload rewrites vec(memtype) with exactly one min-only limit.
func boundMemoryPayload(payload []byte, maxPages uint32) ([]byte, error) {
	count, n := binary.Uvarint(payload)
	if n <= 0 || count != 1 || len(payload) <= n || payload[n] != limitsMinOnly {
		return nil, errors.New("expected exactly one memory without a maximum")
	}
	minPages, m := binary.Uvarint(payload[n+1:])
	if m <= 0 {
		return nil, errors.New("bad memory minimum")
	}
	if uint64(maxPages) < minPages {
		return nil, fmt.Errorf("max pages %d below minimum %d", maxPages, minPages)
	}
	patched := binary.AppendUvarint(nil, 1)
	patched = append(patched, limitsMinAndMax)
	patched = binary.AppendUvarint(patched, minPages)
	return binary.AppendUvarint(patched, uint64(maxPages)), nil
}
