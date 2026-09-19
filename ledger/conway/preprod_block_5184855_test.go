package conway

import (
	"os"
	"testing"

	"github.com/blinklabs-io/gouroboros/ledger/common"
)

// Preprod block 5,184,855 (2026-09-16 19:03:39 UTC). Its first transaction
// carries a native `atLeast` script with a threshold of -1, which the node
// accepted. Typing the threshold as uint made the whole block undecodable,
// and every Sundae consumer of the sync stream retried it without end.
func TestPreprodBlock5184855Decodes(t *testing.T) {
	raw, err := os.ReadFile("testdata/preprod-block-5184855.cbor")
	if err != nil {
		t.Fatal(err)
	}
	// The archive stores the block as [era, block]; the block is the second
	// element, so skip the two-element array header and the era byte.
	if len(raw) < 2 || raw[0] != 0x82 {
		t.Fatalf("expected a 2-element array wrapper, got %x", raw[:1])
	}
	blockCbor := raw[2:]
	block, err := NewConwayBlockFromCbor(blockCbor)
	if err != nil {
		t.Fatalf("block did not decode: %v", err)
	}
	if got := block.SlotNumber(); got != 133902219 {
		t.Fatalf("slot = %d, want 133902219", got)
	}
	txs := block.Transactions()
	if len(txs) != 2 {
		t.Fatalf("txs = %d, want 2", len(txs))
	}
	scripts := block.TransactionWitnessSets[0].NativeScripts()
	if len(scripts) != 1 {
		t.Fatalf("native scripts in tx 0 = %d, want 1", len(scripts))
	}
	nofk, ok := scripts[0].Item().(*common.NativeScriptNofK)
	if !ok {
		t.Fatalf("script is %T, want *NativeScriptNofK", scripts[0].Item())
	}
	if nofk.N != -1 {
		t.Fatalf("threshold = %d, want -1", nofk.N)
	}
}
