// Package bnk implements access to the Wwise SoundBank file format.
package bnk

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

// A File represents an open Wwise SoundBank.
type File struct {
	closer            io.Closer
	BankHeaderSection *BankHeaderSection
	IndexSection      *DataIndexSection
	DataSection       *DataSection
	Others            []*UnknownSection
}

// A ReplacementWem defines a wem to be replaced into an original SoundBank File.
type ReplacementWem struct {
	// The reader pointing to the contents of the new wem.
	Wem io.ReaderAt
	// The index, where zero is the first wem, into the original SoundBank's wems
	// to replace.
	WemIndex int
	// The number of bytes to read in for this wem.
	Length int64
}

type ReplacementWems []*ReplacementWem

// ByWemIndex implements the sort.Interface for sorting a slice of
// ReplacementWems in ascending order of their WemIndex.
type ByWemIndex struct {
	ReplacementWems
}

// NewFile creates a new File for access Wwise SoundBank files. The file is
// expected to start at position 0 in the io.ReaderAt.
func NewFile(r io.ReaderAt) (*File, error) {
	bnk := new(File)

	sr := io.NewSectionReader(r, 0, math.MaxInt64)
	for {
		hdr := new(SectionHeader)
		err := binary.Read(sr, binary.LittleEndian, hdr)
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}

		switch id := hdr.Identifier; id {
		case bkhdHeaderId:
			sec, err := hdr.NewBankHeaderSection(sr)
			if err != nil {
				return nil, err
			}
			bnk.BankHeaderSection = sec
		case didxHeaderId:
			sec, err := hdr.NewDataIndexSection(sr)
			if err != nil {
				return nil, err
			}
			bnk.IndexSection = sec
		case dataHeaderId:
			sec, err := hdr.NewDataSection(sr, bnk.IndexSection)
			if err != nil {
				return nil, err
			}
			bnk.DataSection = sec
		default:
			sec, err := hdr.NewUnknownSection(sr)
			if err != nil {
				return nil, err
			}
			bnk.Others = append(bnk.Others, sec)
		}
	}

	if bnk.DataSection == nil {
		return nil, errors.New("There are no wems stored within this SoundBank.")
	}

	return bnk, nil
}

// WriteTo writes the full contents of this File to the Writer specified by w.
func (bnk *File) WriteTo(w io.Writer) (written int64, err error) {
	written, err = bnk.BankHeaderSection.WriteTo(w)
	if err != nil {
		return
	}
	n, err := bnk.IndexSection.WriteTo(w)
	if err != nil {
		return
	}
	written += n
	n, err = bnk.DataSection.WriteTo(w)
	if err != nil {
		return
	}
	written += n
	for _, other := range bnk.Others {
		n, err = other.WriteTo(w)
		if err != nil {
			return
		}
		written += n
	}
	return written, err
}

// Open opens the File at the specified path using os.Open and prepares it for
// use as a Wwise SoundBank file.
func Open(path string) (*File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	bnk, err := NewFile(f)
	if err != nil {
		f.Close()
		return nil, err
	}
	bnk.closer = f
	return bnk, nil
}

// Close closes the File
// If the File was created using NewFile directly instead of Open,
// Close has no effect.
func (bnk *File) Close() error {
	var err error
	if bnk.closer != nil {
		err = bnk.closer.Close()
		bnk.closer = nil
	}
	return err
}

// ReplaceWem replaces the wem of File at index i, reading the wem, with
// specified length in from r.
func (bnk *File) ReplaceWems(rs ...*ReplacementWem) {
	sort.Sort(ByWemIndex{rs})
	surplus := int64(0)
	for i, r := range rs {
		newLength := r.Length
		wem := bnk.DataSection.Wems[r.WemIndex]
		oldDesc := wem.Descriptor
		oldLength := int64(oldDesc.Length)

		diff := oldLength - newLength
		wem.Reader = io.NewSectionReader(r.Wem, 0, newLength)
		remaining := int64(diff) + wem.RemainingLength

		if newLength > oldLength {
			surplus += newLength - oldLength
			// Take up any remaining padding space if we have a surplus
			if remaining >= surplus {
				remaining, surplus = remaining-surplus, 0
			} else {
				remaining, surplus = 0, surplus-remaining
			}
		}

		// Update the length of the descriptor. This, by pointer dereference,
		// updates the descriptor stored in the IndexSection's DescriptorMap, as
		// well.
		wem.Descriptor.Length = uint32(newLength)
		wem.RemainingReader = io.NewSectionReader(&InfiniteReaderAt{0}, 0,
			remaining)

		lastWi := bnk.IndexSection.WemCount - 1
		for wi := r.WemIndex + 1; surplus > 0 && wi != lastWi; wi++ {
			// There is a surplus, we're not at the last possible wem and we're not the
			// next replacement wem; update shift offsets to respect the new length of
			// r.
			wem := bnk.DataSection.Wems[wi]
			wem.Descriptor.Offset += uint32(surplus)
			if i+1 < len(rs) && wi == rs[i+1].WemIndex {
				// We have just replaced the offset for the next replacement wem. Stop
				// ammending offsets in case the next replacement wem can absorb the
				// surplus in it's padding.
				break
			}
		}
	}
}

func (bnk *File) String() string {
	b := new(strings.Builder)

	b.WriteString(bnk.BankHeaderSection.String())
	b.WriteString(bnk.IndexSection.String())
	b.WriteString(bnk.DataSection.String())

	for _, sec := range bnk.Others {
		b.WriteString(sec.String())
	}

	return b.String()
}

func (rs ReplacementWems) Len() int {
	return len(rs)
}

func (rs ReplacementWems) Swap(i, j int) {
	rs[i], rs[j] = rs[j], rs[i]
}

func (b ByWemIndex) Less(i, j int) bool {
	return b.ReplacementWems[i].WemIndex < b.ReplacementWems[j].WemIndex
}
