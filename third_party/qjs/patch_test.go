package qjs

import "testing"

// mcpgw patch: New with the same wasm slice must reuse the compiled module without
// rehashing, and a different slice with the same content must still be accepted.
func TestNewReusesCompiledModuleForSameBytes(t *testing.T) {
	first, err := New()
	if err != nil {
		t.Fatal(err)
	}
	first.Close()
	compiled := compiledQJSModule
	cachedBytesHash = 0 // a rehash would detect the mismatch and recompile

	second, err := New()
	if err != nil {
		t.Fatal(err)
	}
	second.Close()
	if compiledQJSModule != compiled || cachedBytesHash != 0 {
		t.Fatal("same bytes were hashed and recompiled")
	}

	copied := append([]byte(nil), wasmBytes...)
	third, err := New(Option{QuickJSWasmBytes: copied})
	if err != nil {
		t.Fatal(err)
	}
	third.Close()
	if cachedBytesHash != hashBytes(wasmBytes) {
		t.Fatal("a different slice must go through the hash check")
	}
}

func BenchmarkNewSameBytes(b *testing.B) {
	for i := 0; i < b.N; i++ {
		rt, err := New()
		if err != nil {
			b.Fatal(err)
		}
		rt.Close()
	}
}
