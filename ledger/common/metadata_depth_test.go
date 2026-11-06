package common

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/blinklabs-io/gouroboros/cbor"
)

// nestedListMetadatum builds a metadatum of `depth` single-element lists
// around the unsigned integer 0: 81 81 81 ... 00.
func nestedListMetadatum(depth int) []byte {
	b := bytes.Repeat([]byte{0x81}, depth)
	return append(b, 0x00)
}

// A 16 KB chain of single-element lists is a valid metadatum the node
// accepts (preview block 4,821,137, slot 127,055,999, label 1). Interpreting
// it cost 21 s and 450 MB before the depth bound, because every level
// re-validated the whole subtree below it. The bound must refuse it fast.
func TestDecodeMetadatumRawRefusesDeepNestingQuickly(t *testing.T) {
	start := time.Now()
	_, err := DecodeMetadatumRaw(nestedListMetadatum(16000))
	elapsed := time.Since(start)
	if !errors.Is(err, ErrMetadatumTooDeep) {
		t.Fatalf("err = %v, want ErrMetadatumTooDeep", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("refusal took %v, want well under 2s", elapsed)
	}
}

func TestDecodeMetadatumRawAcceptsNestingAtTheBound(t *testing.T) {
	md, err := DecodeMetadatumRaw(nestedListMetadatum(MaxMetadatumDepth))
	if err != nil {
		t.Fatalf("depth %d should decode, got %v", MaxMetadatumDepth, err)
	}
	if md.TypeName() != "list" {
		t.Fatalf("type = %s, want list", md.TypeName())
	}
	if _, err := DecodeMetadatumRaw(nestedListMetadatum(MaxMetadatumDepth + 1)); !errors.Is(err, ErrMetadatumTooDeep) {
		t.Fatalf("depth %d should be refused, got %v", MaxMetadatumDepth+1, err)
	}
}

// The block-level metadata set must keep the raw bytes of a too-deep
// metadatum and leave its parsed form absent, instead of failing the whole
// block: one such transaction stalled every consumer of the preview sync
// stream for three days.
func TestTransactionMetadataSetKeepsRawOfTooDeepMetadatum(t *testing.T) {
	// {7: {1: <deep list>}, 8: {674: {"msg": ["ok"]}}}
	deep := append([]byte{0xa1, 0x01}, nestedListMetadatum(16000)...)
	fine := []byte{0xa1, 0x19, 0x02, 0xa2, 0xa1, 0x63, 'm', 's', 'g', 0x81, 0x62, 'o', 'k'}
	set := map[uint]cbor.RawMessage{7: deep, 8: fine}
	raw, err := cbor.Encode(set)
	if err != nil {
		t.Fatal(err)
	}
	var s TransactionMetadataSet
	if _, err := cbor.Decode(raw, &s); err != nil {
		t.Fatalf("set did not decode: %v", err)
	}
	if _, ok := s.GetRawMetadata(7); !ok {
		t.Fatal("raw metadata for tx 7 should be kept")
	}
	if _, ok := s.GetMetadata(7); ok {
		t.Fatal("parsed metadata for tx 7 should be absent")
	}
	if md, ok := s.GetMetadata(8); !ok || md.TypeName() != "map" {
		t.Fatalf("tx 8 metadata = %v %v, want a map", md, ok)
	}
}
