package fonts

import (
	"bytes"
	"encoding/binary"
	"sort"
)

// padOutlines aligns each outline to 4 bytes and records the running loca.
func padOutlines(outlines [][]byte, loca []uint32) [][]byte {
	cur := 0
	padded := make([][]byte, len(outlines))

	for idx, o := range outlines {
		loca[idx] = uint32(cur) //nolint:gosec // cumulative glyf size stays far below uint32

		page := bytes.Clone(o)
		for len(page)%4 != 0 {
			page = append(page, 0)
		}

		padded[idx] = page
		cur += len(page)
	}

	loca[len(outlines)] = uint32(cur) //nolint:gosec // cumulative glyf size stays far below uint32

	return padded
}

func encodeUint32Slice(v []uint32) []byte {
	b := make([]byte, len(v)*uint32Bytes)
	for i, x := range v {
		binary.BigEndian.PutUint32(b[i*uint32Bytes:], x)
	}

	return b
}

// buildFontFile assembles an sfnt with the given tables, sorted by tag, with
// proper checksumAdjustment in head.
func buildFontFile(tables []struct {
	tag  string
	data []byte
},
) ([]byte, error) {
	// drop nil tables
	tmp := make([]struct {
		tag  string
		data []byte
	}, 0, len(tables))

	for _, x := range tables {
		if x.data != nil {
			tmp = append(tmp, x)
		}
	}

	sort.Slice(tmp, func(i, j int) bool { return tmp[i].tag < tmp[j].tag })
	num := len(tmp)
	// compute head checksum adjustment: total file length must be 0 mod 2^32
	dirLen := sfntOffsetTableSize + sfntTableRecordSize*num
	// align each table to 4 bytes
	total := dirLen
	aligned := make([]int, num)

	for i, x := range tmp {
		pad := (sfntTableAlign - total%sfntTableAlign) % sfntTableAlign
		total += pad
		aligned[i] = pad
		total += len(x.data)
	}

	headIdx := -1

	for i, x := range tmp {
		if x.tag == "head" {
			headIdx = i
		}
	}

	if headIdx >= 0 {
		return patchHeadChecksum(tmp, aligned, headIdx)
	}

	return assembleFile(tmp, aligned), nil
}

// patchHeadChecksum lays out the file, then rewrites head.checksumAdjustment
// so the whole file checksum equals sfntHeadCheckAdj.
func patchHeadChecksum(tmp []struct {
	tag  string
	data []byte
},
	aligned []int,
	headIdx int,
) ([]byte, error) {
	// checksum of the file with checksumAdjustment zeroed
	zeroed := make([]byte, len(tmp[headIdx].data))
	copy(zeroed, tmp[headIdx].data)
	copy(zeroed[8:12], []byte{0, 0, 0, 0})
	tmp[headIdx].data = zeroed
	zeroedSum := checksum(zeroed)

	// layout the whole file to compute checksum
	full := assembleFile(tmp, aligned)
	sum := checksum(full)
	// place adjustment such that the final file sums to 0xB1B0AFBA.
	// The head checksum in the directory is kept at the zeroed-head
	// value, so the adjustment only shifts the sum once.
	adj := sfntHeadCheckAdj - sum
	adjusted := bytes.Clone(zeroed)
	binary.BigEndian.PutUint32(adjusted[8:12], adj)
	tmp[headIdx].data = adjusted
	full = assembleFile(tmp, aligned)
	// freeze the directory entry for head to the zeroed-head checksum
	for i := range tmp {
		if tmp[i].tag == "head" {
			rec := sfntOffsetTableSize + sfntTableRecordSize*i + uint32Bytes
			binary.BigEndian.PutUint32(full[rec:rec+4], zeroedSum)

			break
		}
	}

	return full, nil
}

func assembleFile(tables []struct {
	tag  string
	data []byte
}, aligned []int,
) []byte {
	num := len(tables)
	buf := new(bytes.Buffer)
	buf.Write([]byte{0, 1, 0, 0})
	writeSFNTHeader(buf, num)

	// directory
	offset := sfntOffsetTableSize + sfntTableRecordSize*num
	for i, posX := range tables {
		offset += aligned[i] // padding before table i

		buf.WriteString(posX.tag)

		var cs [4]byte

		binary.BigEndian.PutUint32(cs[:], checksum(posX.data))
		buf.Write(cs[:])

		var off [4]byte

		binary.BigEndian.PutUint32(off[:], uint32(offset)) //nolint:gosec // sfnt offsets stay far below uint32
		buf.Write(off[:])

		var tlen [4]byte

		binary.BigEndian.PutUint32(tlen[:], uint32(len(posX.data))) //nolint:gosec // table sizes stay far below uint32
		buf.Write(tlen[:])

		offset += len(posX.data)
	}
	// table data with alignment
	for i, x := range tables {
		for range aligned[i] {
			buf.WriteByte(0)
		}

		buf.Write(x.data)
	}

	return buf.Bytes()
}

func writeSFNTHeader(buf *bytes.Buffer, num int) {
	var numT [2]byte

	binary.BigEndian.PutUint16(numT[:], uint16(num)) //nolint:gosec // sfnt table count is small
	buf.Write(numT[:])
	// searchRange, entrySelector, rangeShift
	maxPow := 1
	sel := 0

	for maxPow*2 <= num {
		maxPow *= 2
		sel++
	}

	var sr [2]byte

	binary.BigEndian.PutUint16(sr[:], uint16(maxPow*sfntSearchRangeMul)) //nolint:gosec // table count is small
	buf.Write(sr[:])

	var es [2]byte

	binary.BigEndian.PutUint16(es[:], uint16(sel)) //nolint:gosec // table count is small
	buf.Write(es[:])

	var rs [2]byte

	//nolint:gosec // table count is small
	binary.BigEndian.PutUint16(rs[:], uint16(num*sfntSearchRangeMul-maxPow*sfntSearchRangeMul))
	buf.Write(rs[:])
}

func checksum(buf []byte) uint32 {
	sum := uint32(0)
	for i := 0; i+4 <= len(buf); i += 4 {
		sum += binary.BigEndian.Uint32(buf[i : i+4])
	}

	if rem := len(buf) % sfntTableAlign; rem != 0 {
		tail := make([]byte, sfntTableAlign)
		copy(tail[sfntTableAlign-rem:], buf[len(buf)-rem:])
		sum += binary.BigEndian.Uint32(tail)
	}

	return sum
}
