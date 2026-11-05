package iostore

import "testing"

// TestChunkTypeMapping pins the engine's numbering: the type lives in byte 11 of the id and is read without any
// masking or adjustment. A wrong mapping changes what the loader does with a chunk.
func TestChunkTypeMapping(t *testing.T) {
	cases := []struct {
		raw  byte
		want ChunkType
		name string
	}{
		{0, ChunkInvalid, "Invalid"},
		{1, ChunkExportBundleData, "ExportBundleData"},
		{2, ChunkBulkData, "BulkData"},
		{5, ChunkScriptObjects, "ScriptObjects"},
		{6, ChunkContainerHeader, "ContainerHeader"},
		{7, ChunkExternalFile, "ExternalFile"},
		{9, ChunkShaderCode, "ShaderCode"},
		{10, ChunkPackageStoreEntry, "PackageStoreEntry"},
	}
	for _, c := range cases {
		id := NewChunkID(0x0102030405060708, c.want)
		if id[11] != c.raw {
			t.Fatalf("%s: byte 11 = %d, want %d", c.name, id[11], c.raw)
		}
		if got := TypeOf(id); got != c.want {
			t.Fatalf("%s: TypeOf = %v, want %v", c.name, got, c.want)
		}
		if got := c.want.String(); got != c.name {
			t.Fatalf("String = %q, want %q", got, c.name)
		}
	}
}

// TestFIoHashVectors checks the digest against the published BLAKE3 test vectors before it is trusted anywhere.
func TestFIoHashVectors(t *testing.T) {
	if !FIoHashAvailable() {
		t.Skip("no BLAKE3 in this build")
	}
	if got := FIoHash(nil); fmtHex(got) != "af1349b9f5f9a1a6a0404dea36dcc9499bcb25c9" {
		t.Fatalf("empty digest = %s", fmtHex(got))
	}
	if got := FIoHash([]byte("abc")); fmtHex(got) != "6437b3ac38465133ffb63b75273a8db548c55846" {
		t.Fatalf("abc digest = %s", fmtHex(got))
	}
}

func fmtHex(b [20]byte) string {
	const hex = "0123456789abcdef"
	out := make([]byte, 0, 40)
	for _, c := range b {
		out = append(out, hex[c>>4], hex[c&0xF])
	}
	return string(out)
}
