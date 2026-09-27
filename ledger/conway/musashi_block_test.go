package conway

import (
	"os"
	"testing"

	"github.com/blinklabs-io/gouroboros/cbor"
)

// Musashi (Leios prototype devnet) block 4,254 is the last Conway era block of
// that chain. Like every Musashi header it appends
// block_body_contains_leios_cert and eb_announcement to the ten Babbage header
// body fields.
func TestMusashiConwayBlockWithLeiosHeaderFieldsDecodes(t *testing.T) {
	raw, err := os.ReadFile("testdata/musashi-block-4254.cbor")
	if err != nil {
		t.Fatal(err)
	}
	// [era, block]
	if len(raw) < 2 || raw[0] != 0x82 || raw[1] != BlockTypeConway {
		t.Fatalf("expected a [7, block] wrapper, got %x", raw[:2])
	}
	block, err := NewConwayBlockFromCbor(raw[2:])
	if err != nil {
		t.Fatalf("block did not decode: %v", err)
	}
	if got := block.BlockNumber(); got != 4254 {
		t.Fatalf("block number = %d, want 4254", got)
	}
	if got := block.SlotNumber(); got != 86373 {
		t.Fatalf("slot = %d, want 86373", got)
	}
	const wantHash = "802112126cc600a6afc5193a0150aafd9f6563bec28207df7e6c20cc62e95f8e"
	if got := block.Hash().String(); got != wantHash {
		t.Fatalf("hash = %s, want %s", got, wantHash)
	}
	// The KES-signed header body keeps its original twelve field encoding.
	var headerParts []cbor.RawMessage
	if _, err := cbor.Decode(block.BlockHeader.Cbor(), &headerParts); err != nil {
		t.Fatal(err)
	}
	var bodyItems []cbor.RawMessage
	if _, err := cbor.Decode(block.BlockHeader.Body.Cbor(), &bodyItems); err != nil {
		t.Fatal(err)
	}
	if len(bodyItems) != 12 {
		t.Fatalf("header body has %d fields, want 12", len(bodyItems))
	}
	if string(headerParts[0]) != string(block.BlockHeader.Body.Cbor()) {
		t.Fatal("header body CBOR is not the original encoding")
	}
}
