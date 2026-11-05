package iostore

// ChunkType is EIoChunkType: the high byte of a chunk id's trailing type word. The numbering is the engine's own
// and was verified against its documentation; a chunk carrying package data must not be labelled with the wrong
// member, because the loader dispatches on this value.
type ChunkType uint8

const (
	ChunkInvalid           ChunkType = 0
	ChunkExportBundleData  ChunkType = 1
	ChunkBulkData          ChunkType = 2
	ChunkOptionalBulkData  ChunkType = 3
	ChunkMemoryMappedBulk  ChunkType = 4
	ChunkScriptObjects     ChunkType = 5
	ChunkContainerHeader   ChunkType = 6
	ChunkExternalFile      ChunkType = 7
	ChunkShaderCodeLibrary ChunkType = 8
	ChunkShaderCode        ChunkType = 9
	ChunkPackageStoreEntry ChunkType = 10
	ChunkDerivedData       ChunkType = 11
	ChunkEditorDerivedData ChunkType = 12
	ChunkPackageResource   ChunkType = 13
)

// String names the type the engine would dispatch on.
func (t ChunkType) String() string {
	switch t {
	case ChunkExportBundleData:
		return "ExportBundleData"
	case ChunkBulkData:
		return "BulkData"
	case ChunkOptionalBulkData:
		return "OptionalBulkData"
	case ChunkMemoryMappedBulk:
		return "MemoryMappedBulkData"
	case ChunkScriptObjects:
		return "ScriptObjects"
	case ChunkContainerHeader:
		return "ContainerHeader"
	case ChunkExternalFile:
		return "ExternalFile"
	case ChunkShaderCodeLibrary:
		return "ShaderCodeLibrary"
	case ChunkShaderCode:
		return "ShaderCode"
	case ChunkPackageStoreEntry:
		return "PackageStoreEntry"
	case ChunkDerivedData:
		return "DerivedData"
	case ChunkEditorDerivedData:
		return "EditorDerivedData"
	case ChunkPackageResource:
		return "PackageResource"
	}
	return "Invalid"
}

// TypeOf reads the chunk type from a raw 12-byte id: the type word occupies bytes 8..11, so the member sits in
// byte 11. Nothing is masked or shifted down by one.
func TypeOf(id ChunkID) ChunkType { return ChunkType(id[11]) }

// NewChunkID builds an id from eight identifying bytes and a type.
func NewChunkID(id uint64, t ChunkType) ChunkID {
	var out ChunkID
	for i := 0; i < 8; i++ {
		out[i] = byte(id >> (8 * i))
	}
	out[11] = byte(t)
	return out
}
