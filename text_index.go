package main

import (
	"sort"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/apstndb/go-lsp-export/protocol"
)

// textIndex converts between UTF-8 byte offsets used by memefish and the
// UTF-16 code-unit positions required by LSP's default position encoding.
type textIndex struct {
	text       string
	lineStarts []int
}

func newTextIndex(text string) textIndex {
	lineStarts := []int{0}
	for i := range len(text) {
		if text[i] == '\n' {
			lineStarts = append(lineStarts, i+1)
		}
	}
	return textIndex{text: text, lineStarts: lineStarts}
}

func (index textIndex) position(byteOffset int) protocol.Position {
	byteOffset = min(max(byteOffset, 0), len(index.text))
	line := sort.Search(len(index.lineStarts), func(i int) bool {
		return index.lineStarts[i] > byteOffset
	}) - 1
	lineStart := index.lineStarts[max(line, 0)]
	return protocol.Position{
		Line:      uint32(max(line, 0)),
		Character: uint32(utf16Length(index.text[lineStart:byteOffset])),
	}
}

func utf16Length(text string) int {
	length := 0
	for _, r := range text {
		length += utf16.RuneLen(r)
	}
	return length
}

func (index textIndex) byteOffset(pos protocol.Position) (int, bool) {
	line := int(pos.Line)
	if line < 0 || line >= len(index.lineStarts) {
		return len(index.text), false
	}
	start := index.lineStarts[line]
	end := len(index.text)
	if line+1 < len(index.lineStarts) {
		end = index.lineStarts[line+1] - 1
	}

	target := int(pos.Character)
	units := 0
	offset := start
	for offset < end {
		if units == target {
			return offset, true
		}
		r, size := utf8.DecodeRuneInString(index.text[offset:end])
		runeUnits := utf16.RuneLen(r)
		if units+runeUnits > target {
			return offset, false
		}
		units += runeUnits
		offset += size
	}
	if units == target {
		return end, true
	}
	return end, false
}

func (index textIndex) rangeByByteOffsets(start, end int) protocol.Range {
	return protocol.Range{
		Start: index.position(start),
		End:   index.position(end),
	}
}

func (index textIndex) singleLineUTF16Range(start, end int) (line, character, length int, ok bool) {
	if start < 0 || end < start || end > len(index.text) {
		return 0, 0, 0, false
	}
	r := index.rangeByByteOffsets(start, end)
	if r.Start.Line != r.End.Line || comparePosition(r.Start, r.End) > 0 {
		return 0, 0, 0, false
	}
	return int(r.Start.Line), int(r.Start.Character), int(r.End.Character - r.Start.Character), true
}
