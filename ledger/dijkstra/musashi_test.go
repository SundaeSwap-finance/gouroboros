// Copyright 2026 Blink Labs Software
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package dijkstra

import (
	"fmt"
	"os"
	"testing"

	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/stretchr/testify/require"
)

// The testdata/musashi-block-<height>.cbor fixtures are blocks of the Leios
// prototype Musashi devnet as archived by sundae-sync-v2 (or served by Dolos),
// each the CBOR array [era, block]. Expected transaction hashes and outputs
// were cross-checked against cardano-db-sync for the same chain.

func loadMusashiBlock(
	t *testing.T,
	height uint64,
	config ...common.VerifyConfig,
) (*DijkstraBlock, []byte) {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("testdata/musashi-block-%d.cbor", height))
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(raw), 2)
	// [era, block]: skip the two-element array header and the era byte.
	require.Equal(t, byte(0x82), raw[0])
	require.Equal(t, byte(BlockTypeDijkstra), raw[1])
	blockCbor := raw[2:]
	block, err := NewDijkstraBlockFromCbor(blockCbor, config...)
	require.NoError(t, err)
	return block, blockCbor
}

var skipBodyHash = common.VerifyConfig{SkipBodyHashValidation: true}

func TestMusashiBlocksDecode(t *testing.T) {
	testCases := []struct {
		height    uint64
		slot      uint64
		hash      string
		txCount   int
		certifies bool
		txHashes  map[int]string
	}{
		{
			height: 4255,
			slot:   86463,
			hash:   "d0c2a26a0192baf397b75cd38137987d82036c269089362842888279f3e19daf",
		},
		{
			height:  4277,
			slot:    86855,
			hash:    "adb23531ebb61891912e6a4bdabcbaaa053223d2de342eedbaa9b6af4fb526f3",
			txCount: 1,
			txHashes: map[int]string{
				0: "5b5a7bd58ae56d5e9cff9b82ecbdad5b076b017ff47dfc758e94e201190724e2",
			},
		},
		{
			height:  22000,
			slot:    471105,
			hash:    "3ee810ac76dfe71d02aa34bdd5f947285b78f5f632a3587d35312c69da7bc29c",
			txCount: 9,
			txHashes: map[int]string{
				0: "62e04a3d81ae2e94fe4fc795a0bbe8eb883db209c9f84577ea1a29240c4e40cb",
				4: "e8b345d5d73367dad1f795bdaac7a31969a9d120096abf6f15359fb81dea9c2f",
				8: "6b6d86dc04fb84dfa31fddada49a5e80dffbd2a57ad6bd24aaa7e8f403645e66",
			},
		},
		{
			height:    17410,
			slot:      371958,
			hash:      "724a00a73f2a19d1e14fbbd554de9cbaa24805efaf0aff14ebb7b8ff5620b447",
			txCount:   1066,
			certifies: true,
			txHashes: map[int]string{
				0:    "4f534780add7857db964306a215683df8b3ce1a2f167c33d411e02a8d384735f",
				533:  "4a40998378a759d39b517be4537e7f5ebf243138b82ddf8aaeadbcb2ede1fa28",
				1065: "82bd016c2c72e7faf2ba8769438814b03991b575b233e18b394da4a73cf473b0",
			},
		},
		{
			height:  31704,
			slot:    688043,
			hash:    "cd96f188ce6291271ca6efa7194d5f4911062c847eac08b5cbbd79f58b7c0532",
			txCount: 428,
			txHashes: map[int]string{
				0:   "08e3eda11b7b3635970b5940825bc8edf83e443547de028e5c977684eb0c791f",
				214: "70a21b01922b804c0770ae41f1af6185fa8936d85c66b3963a32d99ed1c89fac",
				427: "412d8be98aa4915fd4c36f090a55e9fe3aa189d355e771fe5c12d97b502b8015",
			},
		},
		{
			height:  34085,
			slot:    742487,
			hash:    "3308becb6ef11e854ce2f145220b64e03f2145573986f0099644830c2e367974",
			txCount: 4,
			txHashes: map[int]string{
				0: "814ca6c7e129025a4dee106565fc09c3326dc2d96a98041522a4a6e5d7ed74dd",
				2: "52def7a796f764e5ab4241ef9ab7574db7d0a76b624d19f8ecfb6baea9a8abf2",
				3: "ace003adc591cfc79e6cfe321a9c889d8283f1127651c087630f001638924d76",
			},
		},
		{
			height:  39059,
			slot:    853600,
			hash:    "0c88b1e221beb608ed37dcfabed93a4a4e8f1ea0ec96f2d93b84c04816d5ddb4",
			txCount: 434,
			txHashes: map[int]string{
				0:   "74e2116ca6e0c809f156aa062a9d4ee8b156322618846f1bdea5d9ad7a206a12",
				217: "44048470f16e6efd78ac67e27579e8022bee3ab1b9c2c32b518c40b66837a3a9",
				433: "011d275ff19fcca3913de17729bf6ed7d0e47d794c29f7c5691cdedcf0e601fe",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(fmt.Sprintf("block %d", tc.height), func(t *testing.T) {
			var config []common.VerifyConfig
			if tc.certifies {
				// A resolved certifying block carries its endorser block's
				// transactions inline, so its body no longer hashes to the
				// header's block_body_hash.
				config = append(config, skipBodyHash)
			}
			block, blockCbor := loadMusashiBlock(t, tc.height, config...)
			require.Equal(t, tc.height, block.BlockNumber())
			require.Equal(t, tc.slot, block.SlotNumber())
			require.Equal(t, tc.hash, block.Hash().String())
			require.Equal(t, EraDijkstra, block.Era())
			require.Equal(t, tc.certifies, block.BlockHeader.BlockBodyContainsLeiosCert)
			require.Equal(t, tc.certifies, block.BlockBody.LeiosCertificate != nil)
			require.Nil(t, block.BlockBody.PerasCertificate)

			// The KES-signed header body is the original twelve field array.
			var headerParts []cbor.RawMessage
			_, err := cbor.Decode(block.BlockHeader.Cbor(), &headerParts)
			require.NoError(t, err)
			require.Equal(t, []byte(headerParts[0]), block.BlockHeader.Body.Cbor())

			txs := block.Transactions()
			require.Len(t, txs, tc.txCount)
			for idx, want := range tc.txHashes {
				require.Equal(t, want, txs[idx].Hash().String(), "tx %d", idx)
			}
			for _, tx := range txs {
				require.True(t, tx.IsValid())
				require.NotEmpty(t, tx.Inputs())
				require.Equal(t, tx.Inputs(), tx.Consumed())
				require.Len(t, tx.Produced(), len(tx.Outputs()))
			}

			// Re-encoding from the decoded parts reproduces the block.
			body := block.BlockBody
			body.DecodeStoreCbor = cbor.DecodeStoreCbor{}
			bodyCbor, err := body.MarshalCBOR()
			require.NoError(t, err)
			var blockParts []cbor.RawMessage
			_, err = cbor.Decode(blockCbor, &blockParts)
			require.NoError(t, err)
			require.Len(t, blockParts, 2)
			require.Equal(t, []byte(blockParts[1]), bodyCbor)
		})
	}
}

func TestMusashiBlockBodyHash(t *testing.T) {
	// An ordinary ranking block passes body hash validation.
	block, _ := loadMusashiBlock(t, 22000)
	require.Equal(t, block.BlockBodyHash(), block.CalculatedBlockBodyHash())

	// A resolved certifying block does not, unless validation is skipped.
	raw, err := os.ReadFile("testdata/musashi-block-17410.cbor")
	require.NoError(t, err)
	_, err = NewDijkstraBlockFromCbor(raw[2:])
	require.ErrorContains(t, err, "body hash mismatch")
}

func TestMusashiHeaderLeiosFields(t *testing.T) {
	block, _ := loadMusashiBlock(t, 4255)
	require.False(t, block.BlockHeader.BlockBodyContainsLeiosCert)
	require.Nil(t, block.BlockHeader.EbAnnouncement)
	require.Equal(
		t,
		"802112126cc600a6afc5193a0150aafd9f6563bec28207df7e6c20cc62e95f8e",
		block.PrevHash().String(),
	)

	block, _ = loadMusashiBlock(t, 17410, skipBodyHash)
	header := block.BlockHeader
	require.True(t, header.BlockBodyContainsLeiosCert)
	require.NotNil(t, header.EbAnnouncement)
	require.Equal(
		t,
		"aa834877783742f842264c73fcdaacdc79c85a1889e00024f5ae2f96bcc462fa",
		header.EbAnnouncement.EbHash.String(),
	)
	require.Equal(t, uint32(23908), header.EbAnnouncement.EbSize)
	require.Equal(
		t,
		"c5bb4ba55319de64664d34f5af638eb5aa0503bd6473f730dafb19e6f77c6e66",
		block.PrevHash().String(),
	)

	cert := block.BlockBody.LeiosCertificate
	require.NotNil(t, cert)
	require.Equal(t, "fb38a17204000a08208000", fmt.Sprintf("%x", cert.Signers))
	require.Len(t, cert.Signature, 48)

	// A header built from its fields encodes the twelve field layout.
	rebuilt := DijkstraBlockHeader{
		BabbageBlockHeader:         header.BabbageBlockHeader,
		BlockBodyContainsLeiosCert: header.BlockBodyContainsLeiosCert,
		EbAnnouncement:             header.EbAnnouncement,
	}
	rebuilt.DecodeStoreCbor = cbor.DecodeStoreCbor{}
	rebuilt.Body.DecodeStoreCbor = cbor.DecodeStoreCbor{}
	rebuiltCbor, err := rebuilt.MarshalCBOR()
	require.NoError(t, err)
	require.Equal(t, header.Cbor(), rebuiltCbor)
}

func TestMusashiPoolRegistrationBlsKey(t *testing.T) {
	block, _ := loadMusashiBlock(t, 4277)
	tx := block.BlockBody.Transactions[0]
	require.Len(t, tx.Body.TxCertificates, 1)
	wrapper := tx.Body.TxCertificates[0]
	require.NotNil(t, wrapper.PoolBlsKey)
	require.Len(t, wrapper.PoolBlsKey.PublicKey, 96)
	require.Len(t, wrapper.PoolBlsKey.PossessionProof, 48)
	certs := tx.Certificates()
	require.Len(t, certs, 1)
	poolReg, ok := certs[0].(*common.PoolRegistrationCertificate)
	require.True(t, ok, "certificate is %T", certs[0])
	require.Equal(t, uint(common.CertificateTypePoolRegistration), poolReg.CertType)
	// The certificate keeps its original eleven element encoding.
	var items []cbor.RawMessage
	_, err := cbor.Decode(poolReg.Cbor(), &items)
	require.NoError(t, err)
	require.Len(t, items, 11)
}

func TestMusashiScriptTransactions(t *testing.T) {
	block, _ := loadMusashiBlock(t, 34085)
	txs := block.Transactions()
	require.Len(t, txs, 4)
	var redeemers, inlineDatums, assetOutputs int
	for _, tx := range txs {
		require.NotEmpty(t, tx.ReferenceInputs())
		require.NotEmpty(t, tx.Collateral())
		require.NotNil(t, tx.CollateralReturn())
		for range tx.Witnesses().Redeemers().Iter() {
			redeemers++
		}
		for _, utxo := range tx.Produced() {
			if utxo.Output.Datum() != nil {
				inlineDatums++
				require.NotNil(t, utxo.Output.DatumHash())
			}
			if utxo.Output.Assets() != nil {
				assetOutputs++
			}
		}
	}
	// Figures from cardano-db-sync for the same block.
	require.Equal(t, 76, redeemers)
	require.Equal(t, 4, inlineDatums)
	require.Positive(t, assetOutputs)

	tx := txs[0]
	out := tx.Outputs()[0]
	require.NotNil(t, out.Datum())
	assets := out.Assets()
	require.NotNil(t, assets)
	require.Len(t, assets.Policies(), 2)
	spends := tx.Witnesses().Redeemers().Indexes(common.RedeemerTagSpend)
	require.NotEmpty(t, spends)
}

func TestMusashiMint(t *testing.T) {
	block, _ := loadMusashiBlock(t, 31704)
	tx := block.Transactions()[115]
	mint := tx.AssetMint()
	require.NotNil(t, mint)
	policies := mint.Policies()
	require.Len(t, policies, 1)
	require.Equal(
		t,
		"1e4fbd563a493922e88c0bfcdece8d03ef8f3d7a7f80641d057f78e2",
		policies[0].String(),
	)
	require.Equal(t, int64(1), mint.Asset(policies[0], []byte{}).Int64())
}

func TestMusashiSubTransaction(t *testing.T) {
	block, _ := loadMusashiBlock(t, 39059)
	tx := block.BlockBody.Transactions[0]
	require.Equal(
		t,
		"74e2116ca6e0c809f156aa062a9d4ee8b156322618846f1bdea5d9ad7a206a12",
		tx.Hash().String(),
	)
	subTxs := tx.Body.TxSubTransactions.Items()
	require.Len(t, subTxs, 1)
	sub := subTxs[0]
	require.Equal(
		t,
		"63ac433eefaed71cf0fa2c7134f82ea38f3c6c338b3d5129c686c47b44162691",
		sub.Body.Id().String(),
	)
	require.Len(t, sub.Body.Inputs(), 1)
	require.Len(t, sub.Body.Outputs(), 1)
	require.NotNil(t, sub.Body.TxGuards)
	require.NotEmpty(t, sub.Body.RequiredSigners())
	require.NotNil(t, sub.WitnessSet.Redeemers())
}

func TestMusashiBlockTransactionIsValidLast(t *testing.T) {
	txCbor, err := cbor.Encode([]any{minimalTxBody(), minimalWitnessSet(), nil, false})
	require.NoError(t, err)
	tx, err := NewDijkstraTransactionFromCbor(txCbor)
	require.NoError(t, err)
	require.False(t, tx.IsValid())
	require.Equal(t, txCbor, tx.Cbor())

	bodyCbor, err := cbor.Encode([]any{[]cbor.RawMessage{txCbor}, nil, nil})
	require.NoError(t, err)
	var body DijkstraBlockBody
	require.NoError(t, body.UnmarshalCBOR(bodyCbor))
	require.Len(t, body.Transactions, 1)
	require.False(t, body.Transactions[0].IsValid())
	require.Equal(t, []uint{0}, body.InvalidTransactions)
	require.Equal(t, common.Blake2b256Hash(bodyCbor), body.Hash())

	// The Conway-style position of is_valid is not a block transaction.
	legacyTx, err := cbor.Encode([]any{minimalTxBody(), minimalWitnessSet(), true, nil})
	require.NoError(t, err)
	bodyCbor, err = cbor.Encode([]any{[]cbor.RawMessage{legacyTx}, nil, nil})
	require.NoError(t, err)
	err = body.UnmarshalCBOR(bodyCbor)
	require.ErrorContains(t, err, "is_valid]")
}

func TestMusashiCertificateEncodings(t *testing.T) {
	leiosCbor, err := cbor.Encode([]any{[]byte{0x01}, make([]byte, 48)})
	require.NoError(t, err)
	var leiosCert DijkstraLeiosCertificate
	require.NoError(t, leiosCert.UnmarshalCBOR(leiosCbor))
	require.Equal(t, []byte{0x01}, leiosCert.Signers)
	require.Len(t, leiosCert.Signature, 48)
	leiosCert.DecodeStoreCbor = cbor.DecodeStoreCbor{}
	encoded, err := leiosCert.MarshalCBOR()
	require.NoError(t, err)
	require.Equal(t, leiosCbor, encoded)

	perasCbor, err := cbor.Encode([]byte{0xde, 0xad})
	require.NoError(t, err)
	var perasCert DijkstraPerasCertificate
	require.NoError(t, perasCert.UnmarshalCBOR(perasCbor))
	require.Equal(t, []byte{0xde, 0xad}, perasCert.Data)
	perasCert.DecodeStoreCbor = cbor.DecodeStoreCbor{}
	encoded, err = perasCert.MarshalCBOR()
	require.NoError(t, err)
	require.Equal(t, perasCbor, encoded)

	// The generated CDDL empty list placeholder is still accepted.
	require.NoError(t, perasCert.UnmarshalCBOR([]byte{0x80}))
	require.Nil(t, perasCert.Data)
}
