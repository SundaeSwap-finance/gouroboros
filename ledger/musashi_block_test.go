package ledger

import (
	"os"
	"testing"

	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/stretchr/testify/require"
)

// Decode archived Musashi (Leios prototype devnet) [era, block] bytes the way
// Sundae consumers do, through NewBlockFromCbor with the era from the wrapper.
func TestNewBlockFromCborMusashi(t *testing.T) {
	testCases := []struct {
		file    string
		txCount int
	}{
		{file: "conway/testdata/musashi-block-4254.cbor"},
		{file: "dijkstra/testdata/musashi-block-4255.cbor"},
		// A certifying ranking block resolved with its endorser block's
		// transactions inline.
		{file: "dijkstra/testdata/musashi-block-17410.cbor", txCount: 1066},
		{file: "dijkstra/testdata/musashi-block-34085.cbor", txCount: 4},
	}
	for _, tc := range testCases {
		t.Run(tc.file, func(t *testing.T) {
			raw, err := os.ReadFile(tc.file)
			require.NoError(t, err)
			block, err := NewBlockFromCbor(
				uint(raw[1]),
				raw[2:],
				common.VerifyConfig{SkipBodyHashValidation: true},
			)
			require.NoError(t, err)
			txs := block.Transactions()
			require.Len(t, txs, tc.txCount)
			for _, tx := range txs {
				require.True(t, tx.IsValid())
				require.NotEmpty(t, tx.Consumed())
				for _, utxo := range tx.Produced() {
					require.Equal(t, tx.Hash(), utxo.Id.Id())
					require.NotNil(t, utxo.Output.Address())
				}
			}
		})
	}
}
