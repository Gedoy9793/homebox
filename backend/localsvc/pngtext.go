package localsvc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
)

// specKeyword is the PNG text chunk the browser looks for. Keep it in step with
// LABEL_SPEC_KEYWORD in frontend/lib/labels/label-spec.ts.
const specKeyword = "homebox:label"

var (
	pngSignature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}

	errNotPNG      = errors.New("localsvc: not a PNG image")
	errInvalidSize = errors.New("localsvc: label size rounds down to an empty image")
)

// embedText inserts an uncompressed iTXt chunk carrying text before the first
// IDAT chunk. iTXt is used because it is UTF-8 by definition, so item names in
// any language survive; tEXt would be limited to Latin-1.
//
// The payload stays uncompressed: a label layout is a couple of kilobytes, and
// leaving it readable makes the image easy to debug with any PNG tool.
func embedText(image []byte, keyword string, text string) ([]byte, error) {
	insertAt, err := firstIDATOffset(image)
	if err != nil {
		return nil, err
	}

	var payload bytes.Buffer
	payload.WriteString(keyword)
	payload.WriteByte(0) // keyword terminator
	payload.WriteByte(0) // compression flag: uncompressed
	payload.WriteByte(0) // compression method
	payload.WriteByte(0) // empty language tag
	payload.WriteByte(0) // empty translated keyword
	payload.WriteString(text)

	chunk := encodeChunk("iTXt", payload.Bytes())

	result := make([]byte, 0, len(image)+len(chunk))
	result = append(result, image[:insertAt]...)
	result = append(result, chunk...)
	result = append(result, image[insertAt:]...)

	return result, nil
}

func encodeChunk(chunkType string, data []byte) []byte {
	chunk := make([]byte, 0, len(data)+12)

	chunk = binary.BigEndian.AppendUint32(chunk, uint32(len(data)))
	chunk = append(chunk, chunkType...)
	chunk = append(chunk, data...)

	return binary.BigEndian.AppendUint32(chunk, crc32.ChecksumIEEE(chunk[4:]))
}

// firstIDATOffset walks the chunk list to find where the pixel data starts.
// Ancillary chunks are allowed both before and after it, but inserting ahead of
// the image data means a reader hits the layout without decoding any pixels.
func firstIDATOffset(image []byte) (int, error) {
	const (
		headerLen = 8
		fieldLen  = 4
	)

	if len(image) < headerLen || !bytes.Equal(image[:headerLen], pngSignature) {
		return 0, errNotPNG
	}

	offset := headerLen
	for offset+2*fieldLen <= len(image) {
		length := int(binary.BigEndian.Uint32(image[offset : offset+fieldLen]))
		chunkType := string(image[offset+fieldLen : offset+2*fieldLen])

		if chunkType == "IDAT" || chunkType == "IEND" {
			return offset, nil
		}

		// length + type + data + crc
		next := offset + 2*fieldLen + length + fieldLen
		if next <= offset || next > len(image) {
			break
		}
		offset = next
	}

	return 0, errNotPNG
}
