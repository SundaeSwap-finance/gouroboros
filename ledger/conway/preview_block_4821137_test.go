package conway

import (
	"os"
	"testing"
	"time"
)

// Preview block 4,821,137 (slot 127,055,999, 2026-11-03 13:20:03 UTC). Its
// eighth transaction (ede279c9…) carries 16 KB of metadata under label 1 that
// is a chain of about 16,000 single-element lists. The block decoded, but
// interpreting that metadatum re-validated the subtree at every level, so
// the block took 21 s and 450 MB to decode: every Lambda consumer of the
// preview sync stream ran out of memory on it for three days. With the depth
// bound the block decodes in well under a second and the transaction simply
// reports no parsed metadata.
func TestPreviewBlock4821137DecodesQuickly(t *testing.T) {
	raw, err := os.ReadFile("testdata/preview-block-4821137.cbor")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 2 || raw[0] != 0x82 {
		t.Fatalf("expected a 2-element array wrapper, got %x", raw[:1])
	}
	start := time.Now()
	block, err := NewConwayBlockFromCbor(raw[2:])
	if err != nil {
		t.Fatalf("block did not decode: %v", err)
	}
	txs := block.Transactions()
	elapsed := time.Since(start)
	if got := block.SlotNumber(); got != 127055999 {
		t.Fatalf("slot = %d, want 127055999", got)
	}
	if len(txs) != 9 {
		t.Fatalf("txs = %d, want 9", len(txs))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("decode took %v, want well under 2s", elapsed)
	}
	bomb := txs[7]
	if got := bomb.Hash().String()[:16]; got != "ede279c9cf7f9d4b" {
		t.Fatalf("tx 7 hash = %s, want ede279c9cf7f9d4b", got)
	}
	if bomb.Metadata() != nil {
		t.Fatal("tx 7 metadata should be absent (too deep to interpret)")
	}
	// Its neighbours keep theirs.
	if txs[0].Metadata() == nil {
		t.Fatal("tx 0 metadata should still decode")
	}
}
