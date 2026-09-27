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
	"crypto/subtle"
	"errors"
	"fmt"
	"iter"
	"maps"
	"math/big"
	"slices"

	"github.com/blinklabs-io/gouroboros/cbor"
	"github.com/blinklabs-io/gouroboros/ledger/alonzo"
	"github.com/blinklabs-io/gouroboros/ledger/babbage"
	"github.com/blinklabs-io/gouroboros/ledger/common"
	"github.com/blinklabs-io/gouroboros/ledger/conway"
	"github.com/blinklabs-io/gouroboros/ledger/shelley"
	"github.com/blinklabs-io/plutigo/data"
	utxorpc "github.com/utxorpc/go-codegen/utxorpc/v1alpha/cardano"
)

const (
	EraIdDijkstra   = 7
	EraNameDijkstra = "Dijkstra"

	// MinProtocolVersionDijkstra is the lowest protocol major version that
	// belongs to the Dijkstra era.
	MinProtocolVersionDijkstra = 12
	// MaxProtocolVersionDijkstra is the highest protocol major version that
	// belongs to the Dijkstra era.
	MaxProtocolVersionDijkstra = 13

	BlockTypeDijkstra = 8

	BlockHeaderTypeDijkstra = 7

	TxTypeDijkstra = 7

	// MaxTxSize is the decode-time Dijkstra transaction CBOR limit. It
	// mirrors the current Cardano max_tx_size until protocol parameters are
	// available for validation.
	MaxTxSize = 16 * 1024
)

var EraDijkstra = common.Era{
	Id:   EraIdDijkstra,
	Name: EraNameDijkstra,
}

func init() {
	common.RegisterEra(EraDijkstra)
}

type DijkstraBlock struct {
	cbor.StructAsArray
	cbor.DecodeStoreCbor
	BlockHeader *DijkstraBlockHeader
	BlockBody   DijkstraBlockBody
}

// UnmarshalCBOR decodes either block layout:
//
//   - the Leios prototype (Musashi) layout, block = [header, block_body]
//     with block_body = [transactions, leios_certificate / nil,
//     peras_certificate / nil] and complete inline transactions;
//   - the generated Dijkstra CDDL layout, block = [header, transaction_bodies,
//     transaction_witness_sets, auxiliary_data_set, invalid_transactions,
//     leios_cert / nil, peras_cert / nil].
func (b *DijkstraBlock) UnmarshalCBOR(cborData []byte) error {
	var items []cbor.RawMessage
	if _, err := cbor.Decode(cborData, &items); err != nil {
		return err
	}
	if len(items) != 2 && len(items) != 7 {
		return fmt.Errorf(
			"invalid Dijkstra block: expected 2 or 7 components, got %d",
			len(items),
		)
	}
	var header DijkstraBlockHeader
	if _, err := cbor.Decode(items[0], &header); err != nil {
		return fmt.Errorf("decode Dijkstra block header: %w", err)
	}
	var body DijkstraBlockBody
	if len(items) == 2 {
		if _, err := cbor.Decode(items[1], &body); err != nil {
			return fmt.Errorf("decode Dijkstra block body: %w", err)
		}
	} else if err := body.decodeComponents(items[1:]); err != nil {
		return err
	}
	b.BlockHeader = &header
	b.BlockBody = body
	b.SetCbor(cborData)
	return nil
}

func (b *DijkstraBlock) MarshalCBOR() ([]byte, error) {
	if b.Cbor() != nil {
		return b.Cbor(), nil
	}
	if b.BlockBody.inlineTransactions {
		return cbor.Encode([]any{b.BlockHeader, b.BlockBody})
	}
	components, err := b.BlockBody.componentCbors()
	if err != nil {
		return nil, err
	}
	items := []any{b.BlockHeader}
	for _, component := range components {
		items = append(items, cbor.RawMessage(component))
	}
	return cbor.Encode(items)
}

func (DijkstraBlock) Type() int {
	return BlockTypeDijkstra
}

func (b *DijkstraBlock) Hash() common.Blake2b256 {
	return b.BlockHeader.Hash()
}

func (b *DijkstraBlock) Header() common.BlockHeader {
	return b.BlockHeader
}

func (b *DijkstraBlock) PrevHash() common.Blake2b256 {
	return b.BlockHeader.PrevHash()
}

func (b *DijkstraBlock) BlockNumber() uint64 {
	return b.BlockHeader.BlockNumber()
}

func (b *DijkstraBlock) SlotNumber() uint64 {
	return b.BlockHeader.SlotNumber()
}

func (b *DijkstraBlock) IssuerVkey() common.IssuerVkey {
	return b.BlockHeader.IssuerVkey()
}

func (b *DijkstraBlock) BlockBodySize() uint64 {
	return b.BlockHeader.BlockBodySize()
}

func (b *DijkstraBlock) Era() common.Era {
	return EraDijkstra
}

func (b *DijkstraBlock) Transactions() []common.Transaction {
	if len(b.BlockBody.Transactions) == 0 &&
		len(b.BlockBody.TransactionBodies) > 0 {
		_ = b.BlockBody.rebuildTransactions()
	}
	ret := make([]common.Transaction, len(b.BlockBody.Transactions))
	for idx := range b.BlockBody.Transactions {
		ret[idx] = &b.BlockBody.Transactions[idx]
	}
	return ret
}

func (b *DijkstraBlock) Utxorpc() (*utxorpc.Block, error) {
	tmpTxs := b.Transactions()
	txs := make([]*utxorpc.Tx, 0, len(tmpTxs))
	for _, t := range tmpTxs {
		tx, err := t.Utxorpc()
		if err != nil {
			return nil, err
		}
		txs = append(txs, tx)
	}
	body := &utxorpc.BlockBody{
		Tx: txs,
	}
	header := &utxorpc.BlockHeader{
		Hash:   b.Hash().Bytes(),
		Height: b.BlockNumber(),
		Slot:   b.SlotNumber(),
	}
	return &utxorpc.Block{
		Body:   body,
		Header: header,
	}, nil
}

func (b *DijkstraBlock) BlockBodyHash() common.Blake2b256 {
	return b.Header().BlockBodyHash()
}

func (b *DijkstraBlock) CalculatedBlockBodyHash() common.Blake2b256 {
	return b.BlockBody.Hash()
}

// DijkstraLeiosCertificate is the nullable Leios certificate slot of a
// Dijkstra block body. Two encodings are accepted:
//
//   - the Leios prototype (Musashi) certificate,
//     leios_certificate = [signers : bytes, signature : bytes .size 48],
//     where signers is a bitfield naming the committee members that voted;
//   - the empty list placeholder of the generated Dijkstra CDDL in
//     IntersectMBO/cardano-ledger at c47305fcf47bd77437b837d0dfb9cb4181bfbc77,
//     leios_cert = [], which leaves Signers and Signature nil.
type DijkstraLeiosCertificate struct {
	cbor.DecodeStoreCbor
	Signers   []byte
	Signature []byte
}

func (c *DijkstraLeiosCertificate) UnmarshalCBOR(cborData []byte) error {
	var items []cbor.RawMessage
	if _, err := cbor.Decode(cborData, &items); err != nil {
		return err
	}
	var tmp DijkstraLeiosCertificate
	switch len(items) {
	case 0:
	case 2:
		if _, err := cbor.Decode(items[0], &tmp.Signers); err != nil {
			return fmt.Errorf("decode Dijkstra Leios certificate signers: %w", err)
		}
		if _, err := cbor.Decode(items[1], &tmp.Signature); err != nil {
			return fmt.Errorf("decode Dijkstra Leios certificate signature: %w", err)
		}
	default:
		return fmt.Errorf(
			"invalid Dijkstra Leios certificate: expected an empty list or [signers, signature], got %d items",
			len(items),
		)
	}
	*c = tmp
	c.SetCbor(cborData)
	return nil
}

func (c DijkstraLeiosCertificate) MarshalCBOR() ([]byte, error) {
	if raw := c.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	if c.Signers == nil && c.Signature == nil {
		return cbor.Encode([]any{})
	}
	return cbor.Encode([]any{c.Signers, c.Signature})
}

// DijkstraPerasCertificate is the nullable Peras certificate slot of a
// Dijkstra block body. The Leios prototype (Musashi) CDDL defines it as
// opaque bytes (peras_certificate = bytes), and the generated Dijkstra CDDL
// as an empty list placeholder. Both are accepted. Data holds the byte string
// content and is nil for the placeholder.
type DijkstraPerasCertificate struct {
	cbor.DecodeStoreCbor
	Data []byte
}

func (c *DijkstraPerasCertificate) UnmarshalCBOR(cborData []byte) error {
	if len(cborData) == 0 {
		return errors.New("empty Dijkstra Peras certificate")
	}
	var tmp DijkstraPerasCertificate
	switch cborData[0] & cbor.CborTypeMask {
	case cbor.CborTypeByteString:
		if _, err := cbor.Decode(cborData, &tmp.Data); err != nil {
			return fmt.Errorf("decode Dijkstra Peras certificate: %w", err)
		}
	default:
		var items []cbor.RawMessage
		if _, err := cbor.Decode(cborData, &items); err != nil {
			return err
		}
		if len(items) != 0 {
			return errors.New(
				"dijkstra Peras certificate must be bytes or an empty list",
			)
		}
	}
	*c = tmp
	c.SetCbor(cborData)
	return nil
}

func (c DijkstraPerasCertificate) MarshalCBOR() ([]byte, error) {
	if raw := c.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	if c.Data == nil {
		return cbor.Encode([]any{})
	}
	return cbor.Encode(c.Data)
}

type DijkstraBlockBody struct {
	cbor.DecodeStoreCbor
	InvalidTransactions    []uint
	Transactions           []DijkstraTransaction
	TransactionBodies      []DijkstraTransactionBody
	TransactionWitnessSets []DijkstraTransactionWitnessSet
	TransactionMetadataSet common.TransactionMetadataSet
	LeiosCertificate       *DijkstraLeiosCertificate
	PerasCertificate       *DijkstraPerasCertificate
	txBodiesCbor           []byte
	txWitnessesCbor        []byte
	txMetadataSetCbor      []byte
	invalidTxsCbor         []byte
	leiosCertificateCbor   []byte
	perasCertificateCbor   []byte
	// inlineTransactions is set for the Leios prototype (Musashi) layout,
	// block_body = [transactions, leios_certificate / nil,
	// peras_certificate / nil], where each transaction is the complete
	// [transaction_body, transaction_witness_set, auxiliary_data / nil,
	// is_valid] array. Only Transactions, InvalidTransactions and the
	// certificates are populated for this layout.
	inlineTransactions     bool
	indefiniteTransactions bool
}

func (b *DijkstraBlockBody) UnmarshalCBOR(cborData []byte) error {
	var items []cbor.RawMessage
	if _, err := cbor.Decode(cborData, &items); err != nil {
		return err
	}
	switch len(items) {
	case 3:
		if err := b.decodeInlineComponents(items); err != nil {
			return err
		}
	case 6:
		if err := b.decodeComponents(items); err != nil {
			return err
		}
	default:
		return fmt.Errorf(
			"invalid Dijkstra block body: expected 3 or 6 components, got %d",
			len(items),
		)
	}
	b.SetCborReference(cborData)
	return nil
}

// decodeInlineComponents decodes the Leios prototype (Musashi) block body.
//
// A ranking block that certifies an endorser block carries no transactions
// of its own on the wire. Node-to-client chain sync (and so Dolos) serves it
// resolved instead: the certified endorser block's transactions are inlined
// into the transactions list. Such a body no longer hashes to the header's
// block_body_hash, so callers decoding resolved blocks must skip body hash
// validation.
func (b *DijkstraBlockBody) decodeInlineComponents(
	items []cbor.RawMessage,
) error {
	var rawTxs []cbor.RawMessage
	if _, err := cbor.Decode(items[0], &rawTxs); err != nil {
		return fmt.Errorf("decode Dijkstra block transactions: %w", err)
	}
	txs := make([]DijkstraTransaction, len(rawTxs))
	var invalidTxs []uint
	for idx, rawTx := range rawTxs {
		var txArray []cbor.RawMessage
		if _, err := cbor.Decode(rawTx, &txArray); err != nil {
			return fmt.Errorf("decode Dijkstra block transaction %d: %w", idx, err)
		}
		if len(txArray) != 4 || !isCborBool(txArray[3]) {
			return fmt.Errorf(
				"invalid Dijkstra block transaction %d: expected [body, witness_set, auxiliary_data / nil, is_valid]",
				idx,
			)
		}
		tx, err := newDijkstraTransactionFromCborComponents(rawTx, txArray, true)
		if err != nil {
			return fmt.Errorf("decode Dijkstra block transaction %d: %w", idx, err)
		}
		if !tx.TxIsValid {
			invalidTxs = append(invalidTxs, uint(idx)) // #nosec G115
		}
		txs[idx] = *tx
	}
	leiosCert, err := decodeDijkstraLeiosCertificate(items[1])
	if err != nil {
		return err
	}
	perasCert, err := decodeDijkstraPerasCertificate(items[2])
	if err != nil {
		return err
	}
	*b = DijkstraBlockBody{
		Transactions:        txs,
		InvalidTransactions: invalidTxs,
		LeiosCertificate:    leiosCert,
		PerasCertificate:    perasCert,
		inlineTransactions:  true,
		// Block producers may write the list with indefinite length.
		indefiniteTransactions: len(items[0]) > 0 && items[0][0] == 0x9f,
	}
	return nil
}

func (b DijkstraBlockBody) encodeInline() ([]byte, error) {
	txs := make([]cbor.RawMessage, len(b.Transactions))
	for idx := range b.Transactions {
		raw, err := b.Transactions[idx].blockTransactionCbor()
		if err != nil {
			return nil, fmt.Errorf("encode Dijkstra block transaction %d: %w", idx, err)
		}
		txs[idx] = raw
	}
	var txsCbor []byte
	if b.indefiniteTransactions {
		txsCbor = []byte{0x9f}
		for _, raw := range txs {
			txsCbor = append(txsCbor, raw...)
		}
		txsCbor = append(txsCbor, 0xff)
	} else {
		var err error
		if txsCbor, err = cbor.Encode(txs); err != nil {
			return nil, err
		}
	}
	var leiosCert, perasCert any
	if b.LeiosCertificate != nil {
		leiosCert = b.LeiosCertificate
	}
	if b.PerasCertificate != nil {
		perasCert = b.PerasCertificate
	}
	return cbor.Encode([]any{cbor.RawMessage(txsCbor), leiosCert, perasCert})
}

func (b DijkstraBlockBody) MarshalCBOR() ([]byte, error) {
	if b.Cbor() != nil {
		return b.Cbor(), nil
	}
	if b.inlineTransactions {
		return b.encodeInline()
	}
	components, err := b.componentCbors()
	if err != nil {
		return nil, err
	}
	items := make([]any, 0, len(components))
	for _, component := range components {
		items = append(items, cbor.RawMessage(component))
	}
	return cbor.Encode(items)
}

// Hash returns the block body hash. In the Leios prototype (Musashi) layout
// it is the blake2b-256 of the body CBOR, and in the generated Dijkstra CDDL
// layout the hash of the concatenated component hashes.
func (b DijkstraBlockBody) Hash() common.Blake2b256 {
	if b.inlineTransactions {
		raw, err := b.MarshalCBOR()
		if err != nil {
			panic("CBOR encoding that should never fail has failed: " + err.Error())
		}
		return common.Blake2b256Hash(raw)
	}
	components, err := b.componentCbors()
	if err != nil {
		panic("CBOR encoding that should never fail has failed: " + err.Error())
	}
	bodyHashes := make([]byte, 0, len(components)*32)
	for _, component := range components {
		componentHash := common.Blake2b256Hash(component)
		bodyHashes = append(bodyHashes, componentHash[:]...)
	}
	return common.Blake2b256Hash(bodyHashes)
}

func (b *DijkstraBlockBody) decodeComponents(items []cbor.RawMessage) error {
	if len(items) != 6 {
		return fmt.Errorf(
			"invalid Dijkstra block body: expected 6 components, got %d",
			len(items),
		)
	}
	var bodies []DijkstraTransactionBody
	if _, err := cbor.Decode(items[0], &bodies); err != nil {
		return fmt.Errorf("decode Dijkstra transaction bodies: %w", err)
	}
	var witnesses []DijkstraTransactionWitnessSet
	if _, err := cbor.Decode(items[1], &witnesses); err != nil {
		return fmt.Errorf("decode Dijkstra transaction witnesses: %w", err)
	}
	if len(bodies) != len(witnesses) {
		return fmt.Errorf(
			"different number of transaction bodies (%d) and witness sets (%d)",
			len(bodies),
			len(witnesses),
		)
	}
	var metadataSet common.TransactionMetadataSet
	if _, err := cbor.Decode(items[2], &metadataSet); err != nil {
		return fmt.Errorf("decode Dijkstra auxiliary data set: %w", err)
	}
	invalidTxs, err := decodeInvalidTransactions(items[3])
	if err != nil {
		return err
	}
	for _, invalidTxIdx := range invalidTxs {
		if invalidTxIdx >= uint(len(bodies)) {
			return fmt.Errorf(
				"invalid transaction index %d outside transaction list length %d",
				invalidTxIdx,
				len(bodies),
			)
		}
	}
	leiosCert, err := decodeDijkstraLeiosCertificate(items[4])
	if err != nil {
		return err
	}
	perasCert, err := decodeDijkstraPerasCertificate(items[5])
	if err != nil {
		return err
	}
	b.TransactionBodies = bodies
	b.TransactionWitnessSets = witnesses
	b.TransactionMetadataSet = metadataSet
	b.InvalidTransactions = invalidTxs
	b.LeiosCertificate = leiosCert
	b.PerasCertificate = perasCert
	b.txBodiesCbor = items[0]
	b.txWitnessesCbor = items[1]
	b.txMetadataSetCbor = items[2]
	b.invalidTxsCbor = items[3]
	b.leiosCertificateCbor = items[4]
	b.perasCertificateCbor = items[5]
	return b.rebuildTransactions()
}

func (b *DijkstraBlockBody) rebuildTransactions() error {
	if len(b.TransactionBodies) != len(b.TransactionWitnessSets) {
		return fmt.Errorf(
			"different number of transaction bodies (%d) and witness sets (%d)",
			len(b.TransactionBodies),
			len(b.TransactionWitnessSets),
		)
	}
	invalidTxMap := make(map[uint]bool, len(b.InvalidTransactions))
	for _, invalidTxIdx := range b.InvalidTransactions {
		if invalidTxIdx >= uint(len(b.TransactionBodies)) {
			return fmt.Errorf(
				"invalid transaction index %d outside transaction list length %d",
				invalidTxIdx,
				len(b.TransactionBodies),
			)
		}
		invalidTxMap[invalidTxIdx] = true
	}
	txs := make([]DijkstraTransaction, len(b.TransactionBodies))
	for idx := range b.TransactionBodies {
		tx := DijkstraTransaction{
			Body:       b.TransactionBodies[idx],
			WitnessSet: b.TransactionWitnessSets[idx],
			TxIsValid:  !invalidTxMap[uint(idx)],
		}
		if raw, ok := b.TransactionMetadataSet.GetRawMetadata(uint(idx)); ok &&
			len(raw) > 0 {
			if err := decodeAuxiliaryDataInto(
				raw,
				&tx.TxMetadata,
				&tx.auxData,
			); err != nil {
				return fmt.Errorf(
					"decode Dijkstra transaction %d auxiliary data: %w",
					idx,
					err,
				)
			}
		} else if metadata, ok := b.TransactionMetadataSet.GetMetadata(uint(idx)); ok {
			tx.TxMetadata = metadata
		}
		txs[idx] = tx
	}
	b.Transactions = txs
	return nil
}

func (b DijkstraBlockBody) componentCbors() ([][]byte, error) {
	if b.hasComponentCbors() {
		return [][]byte{
			b.txBodiesCbor,
			b.txWitnessesCbor,
			b.txMetadataSetCbor,
			b.invalidTxsCbor,
			b.leiosCertificateCbor,
			b.perasCertificateCbor,
		}, nil
	}
	return b.encodeComponentCbors()
}

func (b DijkstraBlockBody) hasComponentCbors() bool {
	return len(b.txBodiesCbor) > 0 &&
		len(b.txWitnessesCbor) > 0 &&
		len(b.txMetadataSetCbor) > 0 &&
		len(b.invalidTxsCbor) > 0 &&
		len(b.leiosCertificateCbor) > 0 &&
		len(b.perasCertificateCbor) > 0
}

func (b DijkstraBlockBody) encodeComponentCbors() ([][]byte, error) {
	bodies, witnesses, metadata, invalidTxs := b.componentsForEncoding()
	if b.LeiosCertificate != nil {
		bodies = []DijkstraTransactionBody{}
		witnesses = []DijkstraTransactionWitnessSet{}
		metadata = map[uint]cbor.RawMessage{}
		invalidTxs = []uint{}
	}
	if len(bodies) != len(witnesses) {
		return nil, fmt.Errorf(
			"different number of transaction bodies (%d) and witness sets (%d)",
			len(bodies),
			len(witnesses),
		)
	}
	txBodiesCbor, err := cbor.Encode(bodies)
	if err != nil {
		return nil, fmt.Errorf("encode Dijkstra transaction bodies: %w", err)
	}
	txWitnessesCbor, err := cbor.Encode(witnesses)
	if err != nil {
		return nil, fmt.Errorf("encode Dijkstra transaction witnesses: %w", err)
	}
	txMetadataSetCbor, err := encodeDijkstraMetadataSet(
		b.TransactionMetadataSet,
		metadata,
	)
	if err != nil {
		return nil, err
	}
	invalidTxsCbor, err := cbor.Encode(invalidTxs)
	if err != nil {
		return nil, fmt.Errorf("encode Dijkstra invalid transactions: %w", err)
	}
	leiosCertificateCbor, err := encodeDijkstraOptionalCertificate(
		b.LeiosCertificate,
	)
	if err != nil {
		return nil, fmt.Errorf("encode Dijkstra Leios certificate: %w", err)
	}
	perasCertificateCbor, err := encodeDijkstraOptionalCertificate(
		b.PerasCertificate,
	)
	if err != nil {
		return nil, fmt.Errorf("encode Dijkstra Peras certificate: %w", err)
	}
	return [][]byte{
		txBodiesCbor,
		txWitnessesCbor,
		txMetadataSetCbor,
		invalidTxsCbor,
		leiosCertificateCbor,
		perasCertificateCbor,
	}, nil
}

func (b DijkstraBlockBody) componentsForEncoding() (
	[]DijkstraTransactionBody,
	[]DijkstraTransactionWitnessSet,
	map[uint]cbor.RawMessage,
	[]uint,
) {
	if len(b.TransactionBodies) > 0 || len(b.TransactionWitnessSets) > 0 {
		invalidTxs := b.InvalidTransactions
		if invalidTxs == nil {
			invalidTxs = []uint{}
		}
		return b.TransactionBodies, b.TransactionWitnessSets, nil, invalidTxs
	}
	txBodies := make([]DijkstraTransactionBody, len(b.Transactions))
	txWitnesses := make([]DijkstraTransactionWitnessSet, len(b.Transactions))
	metadata := make(map[uint]cbor.RawMessage)
	invalidTxs := make([]uint, 0, len(b.InvalidTransactions))
	invalidTxMap := make(map[uint]bool, len(b.InvalidTransactions))
	for _, invalidTxIdx := range b.InvalidTransactions {
		invalidTxMap[invalidTxIdx] = true
	}
	for idx, tx := range b.Transactions {
		txBodies[idx] = tx.Body
		txWitnesses[idx] = tx.WitnessSet
		if !tx.IsValid() && !invalidTxMap[uint(idx)] {
			invalidTxs = append(invalidTxs, uint(idx))
		}
		if tx.auxData != nil && len(tx.auxData.Cbor()) > 0 {
			metadata[uint(idx)] = tx.auxData.Cbor()
		} else if tx.TxMetadata != nil && len(tx.TxMetadata.Cbor()) > 0 {
			metadata[uint(idx)] = tx.TxMetadata.Cbor()
		}
	}
	invalidTxs = append(invalidTxs, b.InvalidTransactions...)
	slices.Sort(invalidTxs)
	return txBodies, txWitnesses, metadata, invalidTxs
}

func encodeDijkstraMetadataSet(
	metadataSet common.TransactionMetadataSet,
	metadata map[uint]cbor.RawMessage,
) ([]byte, error) {
	if raw := metadataSet.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	if metadata == nil {
		metadata = map[uint]cbor.RawMessage{}
	}
	ret, err := cbor.Encode(metadata)
	if err != nil {
		return nil, fmt.Errorf("encode Dijkstra auxiliary data set: %w", err)
	}
	return ret, nil
}

func encodeDijkstraOptionalCertificate(
	cert any,
) ([]byte, error) {
	switch c := cert.(type) {
	case nil:
		return cbor.Encode(nil)
	case *DijkstraLeiosCertificate:
		if c == nil {
			return cbor.Encode(nil)
		}
		return c.MarshalCBOR()
	case *DijkstraPerasCertificate:
		if c == nil {
			return cbor.Encode(nil)
		}
		return c.MarshalCBOR()
	default:
		return nil, fmt.Errorf("unsupported Dijkstra certificate type %T", cert)
	}
}

func decodeDijkstraLeiosCertificate(
	raw cbor.RawMessage,
) (*DijkstraLeiosCertificate, error) {
	if isCborNull(raw) {
		return nil, nil
	}
	var cert DijkstraLeiosCertificate
	if _, err := cbor.Decode(raw, &cert); err != nil {
		return nil, fmt.Errorf("decode Dijkstra Leios certificate: %w", err)
	}
	return &cert, nil
}

func decodeDijkstraPerasCertificate(
	raw cbor.RawMessage,
) (*DijkstraPerasCertificate, error) {
	if isCborNull(raw) {
		return nil, nil
	}
	var cert DijkstraPerasCertificate
	if _, err := cbor.Decode(raw, &cert); err != nil {
		return nil, fmt.Errorf("decode Dijkstra Peras certificate: %w", err)
	}
	return &cert, nil
}

// DijkstraEbAnnouncement is the endorser block a ranking block header
// announces:
//
//	eb_announcement = [eb_hash : hash32, eb_size : uint .size 4]
type DijkstraEbAnnouncement struct {
	cbor.StructAsArray
	EbHash common.Blake2b256
	EbSize uint32
}

// DijkstraBlockHeader is a Dijkstra ranking block header. The header body is
// Babbage's ten fields followed, in the Leios prototype (Musashi) layout, by
//
//	block_body_contains_leios_cert : bool,
//	eb_announcement : eb_announcement / nil
//
// Both the ten and twelve field header bodies are accepted. The trailing
// fields are exposed on the header, and Body.Cbor() holds the original header
// body bytes either way.
type DijkstraBlockHeader struct {
	babbage.BabbageBlockHeader
	BlockBodyContainsLeiosCert bool
	EbAnnouncement             *DijkstraEbAnnouncement
}

func (h *DijkstraBlockHeader) UnmarshalCBOR(cborData []byte) error {
	var tmp DijkstraBlockHeader
	extra, err := babbage.DecodeBlockHeaderWithExtraFields(
		cborData,
		&tmp.BabbageBlockHeader,
	)
	if err != nil {
		return err
	}
	switch len(extra) {
	case 0:
	case 2:
		if _, err := cbor.Decode(extra[0], &tmp.BlockBodyContainsLeiosCert); err != nil {
			return fmt.Errorf(
				"decode Dijkstra block_body_contains_leios_cert: %w",
				err,
			)
		}
		if !isCborNull(extra[1]) {
			tmp.EbAnnouncement = new(DijkstraEbAnnouncement)
			if _, err := cbor.Decode(extra[1], tmp.EbAnnouncement); err != nil {
				return fmt.Errorf("decode Dijkstra eb_announcement: %w", err)
			}
		}
	default:
		return fmt.Errorf(
			"invalid Dijkstra block header body: expected 10 or 12 fields, got %d",
			10+len(extra),
		)
	}
	*h = tmp
	return nil
}

func (h *DijkstraBlockHeader) MarshalCBOR() ([]byte, error) {
	if raw := h.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	body, err := h.HeaderBodyCbor()
	if err != nil {
		return nil, err
	}
	return cbor.Encode([]any{cbor.RawMessage(body), h.Signature})
}

// HeaderBodyCbor returns the CBOR of the header body, the bytes the KES
// signature covers. It is the original encoding when the header was decoded.
// A constructed header encodes the twelve field Leios layout.
func (h *DijkstraBlockHeader) HeaderBodyCbor() ([]byte, error) {
	if raw := h.Body.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	b := h.Body
	var eb any
	if h.EbAnnouncement != nil {
		eb = h.EbAnnouncement
	}
	return cbor.Encode([]any{
		b.BlockNumber,
		b.Slot,
		b.PrevHash,
		b.IssuerVkey,
		b.VrfKey,
		b.VrfResult,
		b.BlockBodySize,
		b.BlockBodyHash,
		b.OpCert,
		b.ProtoVersion,
		h.BlockBodyContainsLeiosCert,
		eb,
	})
}

func (h *DijkstraBlockHeader) Era() common.Era {
	return EraDijkstra
}

type DijkstraTransactionOutput struct {
	cbor.DecodeStoreCbor
	Output common.TransactionOutput
}

func (o *DijkstraTransactionOutput) UnmarshalCBOR(cborData []byte) error {
	if len(cborData) == 0 {
		return errors.New("empty Dijkstra transaction output")
	}
	switch cborData[0] & cbor.CborTypeMask {
	case cbor.CborTypeArray:
		// Match Conway compatibility: historical array-form outputs may still
		// appear, while new outputs use Babbage-style map encoding.
		var tmp alonzo.AlonzoTransactionOutput
		if _, err := cbor.Decode(cborData, &tmp); err != nil {
			return err
		}
		o.Output = &tmp
	case cbor.CborTypeMap:
		var tmp babbage.BabbageTransactionOutput
		if _, err := cbor.Decode(cborData, &tmp); err != nil {
			return err
		}
		o.Output = &tmp
	default:
		return fmt.Errorf(
			"unknown Dijkstra transaction output type: 0x%x",
			cborData[0],
		)
	}
	o.SetCborReference(cborData)
	return nil
}

func (o DijkstraTransactionOutput) MarshalCBOR() ([]byte, error) {
	if raw := o.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	if o.Output == nil {
		return cbor.Encode(nil)
	}
	return cbor.Encode(o.Output)
}

func (o DijkstraTransactionOutput) Address() common.Address {
	return o.Output.Address()
}

func (o DijkstraTransactionOutput) Amount() *big.Int {
	return o.Output.Amount()
}

func (o DijkstraTransactionOutput) Assets() *common.MultiAsset[common.MultiAssetTypeOutput] {
	return o.Output.Assets()
}

func (o DijkstraTransactionOutput) Datum() *common.Datum {
	return o.Output.Datum()
}

func (o DijkstraTransactionOutput) DatumHash() *common.Blake2b256 {
	return o.Output.DatumHash()
}

func (o DijkstraTransactionOutput) ScriptRef() common.Script {
	return o.Output.ScriptRef()
}

func (o DijkstraTransactionOutput) Utxorpc() (*utxorpc.TxOutput, error) {
	return o.Output.Utxorpc()
}

func (o DijkstraTransactionOutput) ToPlutusData() data.PlutusData {
	return o.Output.ToPlutusData()
}

func (o DijkstraTransactionOutput) String() string {
	return o.Output.String()
}

type DijkstraGuards struct {
	cbor.DecodeStoreCbor
	KeyHashes   []common.Blake2b224
	Credentials []common.Credential
}

func (g *DijkstraGuards) UnmarshalCBOR(cborData []byte) error {
	g.SetCbor(cborData)
	var credentials cbor.SetType[common.Credential]
	if _, err := cbor.Decode(cborData, &credentials); err == nil {
		if len(credentials.Items()) == 0 {
			return errors.New("dijkstra guards must not be empty")
		}
		g.Credentials = credentials.Items()
		g.KeyHashes = nil
		return nil
	}
	var keyHashes cbor.SetType[common.Blake2b224]
	if _, err := cbor.Decode(cborData, &keyHashes); err != nil {
		return err
	}
	if len(keyHashes.Items()) == 0 {
		return errors.New("dijkstra guards must not be empty")
	}
	g.KeyHashes = keyHashes.Items()
	g.Credentials = nil
	return nil
}

func (g DijkstraGuards) MarshalCBOR() ([]byte, error) {
	if raw := g.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	if len(g.Credentials) > 0 {
		return cbor.Encode(cbor.NewSetType(g.Credentials, true))
	}
	return cbor.Encode(cbor.NewSetType(g.KeyHashes, true))
}

// DijkstraBlsKey is the optional pool_params BLS key of the Leios prototype:
//
//	bls_key = [bls_pubkey : bytes .size 96, bls_possession_proof : bytes .size 48]
type DijkstraBlsKey struct {
	cbor.StructAsArray
	PublicKey       []byte
	PossessionProof []byte
}

// DijkstraCertificateWrapper decodes a Dijkstra certificate. It matches
// common.CertificateWrapper except that pool_params may carry an optional
// bls_key (bls_key / nil) after the VRF key hash. That field is exposed as
// PoolBlsKey, and the certificate itself decodes to the usual
// *common.PoolRegistrationCertificate with its original CBOR retained.
type DijkstraCertificateWrapper struct {
	common.CertificateWrapper
	PoolBlsKey *DijkstraBlsKey
}

func (c *DijkstraCertificateWrapper) UnmarshalCBOR(data []byte) error {
	certType, err := cbor.DecodeIdFromList(data)
	if err != nil {
		return err
	}
	if certType != int(common.CertificateTypePoolRegistration) {
		c.PoolBlsKey = nil
		return c.CertificateWrapper.UnmarshalCBOR(data)
	}
	var items []cbor.RawMessage
	if _, err := cbor.Decode(data, &items); err != nil {
		return err
	}
	var blsKey *DijkstraBlsKey
	certData := data
	if len(items) == 11 {
		if !isCborNull(items[3]) {
			blsKey = new(DijkstraBlsKey)
			if _, err := cbor.Decode(items[3], blsKey); err != nil {
				return fmt.Errorf("decode pool registration bls_key: %w", err)
			}
		}
		items = slices.Delete(items, 3, 4)
		if certData, err = cbor.Encode(items); err != nil {
			return err
		}
	}
	if err := c.CertificateWrapper.UnmarshalCBOR(certData); err != nil {
		return err
	}
	if cert, ok := c.Certificate.(*common.PoolRegistrationCertificate); ok {
		cert.SetCbor(data)
	}
	c.PoolBlsKey = blsKey
	return nil
}

func (c *DijkstraCertificateWrapper) MarshalCBOR() ([]byte, error) {
	if c.Certificate != nil {
		if raw := c.Certificate.Cbor(); len(raw) > 0 {
			return raw, nil
		}
	}
	return c.CertificateWrapper.MarshalCBOR()
}

type DijkstraRawCbor struct {
	cbor.DecodeStoreCbor
}

func (r *DijkstraRawCbor) UnmarshalCBOR(cborData []byte) error {
	r.SetCbor(cborData)
	return nil
}

func (r DijkstraRawCbor) MarshalCBOR() ([]byte, error) {
	if raw := r.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	return cbor.Encode(nil)
}

type DijkstraTransactionBody struct {
	common.TransactionBodyBase
	TxInputs                   conway.ConwayTransactionInputSet              `cbor:"0,keyasint,omitempty"`
	TxOutputs                  []DijkstraTransactionOutput                   `cbor:"1,keyasint,omitempty"`
	TxFee                      uint64                                        `cbor:"2,keyasint,omitempty"`
	Ttl                        uint64                                        `cbor:"3,keyasint,omitempty"`
	TxCertificates             []DijkstraCertificateWrapper                  `cbor:"4,keyasint,omitempty"`
	TxWithdrawals              map[*common.Address]uint64                    `cbor:"5,keyasint,omitempty"`
	TxAuxDataHash              *common.Blake2b256                            `cbor:"7,keyasint,omitempty"`
	TxValidityIntervalStart    uint64                                        `cbor:"8,keyasint,omitempty"`
	TxMint                     *common.MultiAsset[common.MultiAssetTypeMint] `cbor:"9,keyasint,omitempty"`
	TxScriptDataHash           *common.Blake2b256                            `cbor:"11,keyasint,omitempty"`
	TxCollateral               cbor.SetType[shelley.ShelleyTransactionInput] `cbor:"13,keyasint,omitempty,omitzero"`
	TxGuards                   *DijkstraGuards                               `cbor:"14,keyasint,omitempty"`
	TxNetworkId                *uint8                                        `cbor:"15,keyasint,omitempty"`
	TxCollateralReturn         *DijkstraTransactionOutput                    `cbor:"16,keyasint,omitempty"`
	TxTotalCollateral          uint64                                        `cbor:"17,keyasint,omitempty"`
	TxReferenceInputs          cbor.SetType[shelley.ShelleyTransactionInput] `cbor:"18,keyasint,omitempty,omitzero"`
	TxVotingProcedures         common.VotingProcedures                       `cbor:"19,keyasint,omitempty"`
	TxProposalProcedures       []DijkstraProposalProcedure                   `cbor:"20,keyasint,omitempty"`
	TxCurrentTreasuryValue     uint64                                        `cbor:"21,keyasint,omitempty"`
	TxDonation                 uint64                                        `cbor:"22,keyasint,omitempty"`
	TxSubTransactions          cbor.SetType[DijkstraSubTransaction]          `cbor:"23,keyasint,omitempty,omitzero"`
	TxRequiredTopLevelGuards   *DijkstraRawCbor                              `cbor:"24,keyasint,omitempty"`
	TxDirectDeposits           map[cbor.ByteString]uint64                    `cbor:"25,keyasint,omitempty"`
	TxBalanceIntervals         *DijkstraRawCbor                              `cbor:"26,keyasint,omitempty"`
	TxStartingBalanceIntervals *DijkstraRawCbor                              `cbor:"27,keyasint,omitempty"`
}

func (b *DijkstraTransactionBody) UnmarshalCBOR(cborData []byte) error {
	type tDijkstraTransactionBody DijkstraTransactionBody
	var tmp tDijkstraTransactionBody
	if _, err := cbor.Decode(cborData, &tmp); err != nil {
		return err
	}
	*b = DijkstraTransactionBody(tmp)
	b.SetCborReference(cborData)
	return nil
}

func (b *DijkstraTransactionBody) Inputs() []common.TransactionInput {
	return dijkstraTransactionInputs(b.TxInputs.Items())
}

func (b *DijkstraTransactionBody) Outputs() []common.TransactionOutput {
	return dijkstraTransactionOutputs(b.TxOutputs)
}

func (b *DijkstraTransactionBody) Fee() *big.Int {
	return new(big.Int).SetUint64(b.TxFee)
}

func (b *DijkstraTransactionBody) TTL() uint64 {
	return b.Ttl
}

func (b *DijkstraTransactionBody) ValidityIntervalStart() uint64 {
	return b.TxValidityIntervalStart
}

func (b *DijkstraTransactionBody) ProtocolParameterUpdates() (uint64, map[common.Blake2b224]common.ProtocolParameterUpdate) {
	return 0, nil
}

func (b *DijkstraTransactionBody) Certificates() []common.Certificate {
	return dijkstraCertificates(b.TxCertificates)
}

func (b *DijkstraTransactionBody) Withdrawals() map[*common.Address]*big.Int {
	return dijkstraWithdrawals(b.TxWithdrawals)
}

func (b *DijkstraTransactionBody) AuxDataHash() *common.Blake2b256 {
	return b.TxAuxDataHash
}

func (b *DijkstraTransactionBody) AssetMint() *common.MultiAsset[common.MultiAssetTypeMint] {
	return b.TxMint
}

func (b *DijkstraTransactionBody) Collateral() []common.TransactionInput {
	return dijkstraTransactionInputs(b.TxCollateral.Items())
}

func (b *DijkstraTransactionBody) RequiredSigners() []common.Blake2b224 {
	return dijkstraRequiredSigners(b.TxGuards)
}

func (b *DijkstraTransactionBody) ScriptDataHash() *common.Blake2b256 {
	return b.TxScriptDataHash
}

func (b *DijkstraTransactionBody) ReferenceInputs() []common.TransactionInput {
	return dijkstraReferenceInputs(b.TxReferenceInputs)
}

func (b *DijkstraTransactionBody) CollateralReturn() common.TransactionOutput {
	if b.TxCollateralReturn == nil {
		return nil
	}
	return b.TxCollateralReturn.Output
}

func (b *DijkstraTransactionBody) TotalCollateral() *big.Int {
	return new(big.Int).SetUint64(b.TxTotalCollateral)
}

func (b *DijkstraTransactionBody) VotingProcedures() common.VotingProcedures {
	return b.TxVotingProcedures
}

func (b *DijkstraTransactionBody) ProposalProcedures() []common.ProposalProcedure {
	return dijkstraProposalProcedures(b.TxProposalProcedures)
}

func (b *DijkstraTransactionBody) NetworkId() *uint8 {
	return b.TxNetworkId
}

func (b *DijkstraTransactionBody) CurrentTreasuryValue() *big.Int {
	return new(big.Int).SetUint64(b.TxCurrentTreasuryValue)
}

func (b *DijkstraTransactionBody) Donation() *big.Int {
	return new(big.Int).SetUint64(b.TxDonation)
}

func (b *DijkstraTransactionBody) Utxorpc() (*utxorpc.Tx, error) {
	return common.TransactionBodyToUtxorpc(b)
}

func dijkstraTransactionInputs(
	inputs []shelley.ShelleyTransactionInput,
) []common.TransactionInput {
	ret := make([]common.TransactionInput, 0, len(inputs))
	for _, input := range inputs {
		ret = append(ret, input)
	}
	return ret
}

func dijkstraTransactionOutputs(
	outputs []DijkstraTransactionOutput,
) []common.TransactionOutput {
	ret := make([]common.TransactionOutput, 0, len(outputs))
	for i := range outputs {
		if outputs[i].Output != nil {
			ret = append(ret, outputs[i].Output)
		}
	}
	return ret
}

func dijkstraCertificates(
	certificates []DijkstraCertificateWrapper,
) []common.Certificate {
	ret := make([]common.Certificate, len(certificates))
	for i, cert := range certificates {
		ret[i] = cert.Certificate
	}
	return ret
}

func dijkstraWithdrawals(
	withdrawals map[*common.Address]uint64,
) map[*common.Address]*big.Int {
	if withdrawals == nil {
		return nil
	}
	ret := make(map[*common.Address]*big.Int, len(withdrawals))
	for addr, amount := range withdrawals {
		ret[addr] = new(big.Int).SetUint64(amount)
	}
	return ret
}

func dijkstraRequiredSigners(guards *DijkstraGuards) []common.Blake2b224 {
	if guards == nil {
		return nil
	}
	ret := make([]common.Blake2b224, 0, len(guards.KeyHashes)+len(guards.Credentials))
	ret = append(ret, guards.KeyHashes...)
	for _, cred := range guards.Credentials {
		if cred.CredType == common.CredentialTypeAddrKeyHash {
			ret = append(ret, cred.Credential)
		}
	}
	return ret
}

func dijkstraReferenceInputs(
	referenceInputs cbor.SetType[shelley.ShelleyTransactionInput],
) []common.TransactionInput {
	items := referenceInputs.Items()
	ret := make([]common.TransactionInput, len(items))
	for i := range items {
		ret[i] = &items[i]
	}
	return ret
}

func dijkstraProposalProcedures(
	proposalProcedures []DijkstraProposalProcedure,
) []common.ProposalProcedure {
	ret := make([]common.ProposalProcedure, len(proposalProcedures))
	for i, item := range proposalProcedures {
		ret[i] = item
	}
	return ret
}

type DijkstraSubTransactionBody struct {
	common.TransactionBodyBase
	TxInputs                  conway.ConwayTransactionInputSet              `cbor:"0,keyasint,omitempty"`
	TxOutputs                 []DijkstraTransactionOutput                   `cbor:"1,keyasint,omitempty"`
	Ttl                       uint64                                        `cbor:"3,keyasint,omitempty"`
	TxCertificates            []DijkstraCertificateWrapper                  `cbor:"4,keyasint,omitempty"`
	TxWithdrawals             map[*common.Address]uint64                    `cbor:"5,keyasint,omitempty"`
	TxAuxDataHash             *common.Blake2b256                            `cbor:"7,keyasint,omitempty"`
	TxValidityIntervalStart   uint64                                        `cbor:"8,keyasint,omitempty"`
	TxMint                    *common.MultiAsset[common.MultiAssetTypeMint] `cbor:"9,keyasint,omitempty"`
	TxScriptDataHash          *common.Blake2b256                            `cbor:"11,keyasint,omitempty"`
	TxGuards                  *DijkstraGuards                               `cbor:"14,keyasint,omitempty"`
	TxNetworkId               *uint8                                        `cbor:"15,keyasint,omitempty"`
	TxReferenceInputs         cbor.SetType[shelley.ShelleyTransactionInput] `cbor:"18,keyasint,omitempty,omitzero"`
	TxVotingProcedures        common.VotingProcedures                       `cbor:"19,keyasint,omitempty"`
	TxProposalProcedures      []DijkstraProposalProcedure                   `cbor:"20,keyasint,omitempty"`
	TxCurrentTreasuryValue    uint64                                        `cbor:"21,keyasint,omitempty"`
	TxDonation                uint64                                        `cbor:"22,keyasint,omitempty"`
	TxRequiredTopLevelGuards  *DijkstraRawCbor                              `cbor:"24,keyasint,omitempty"`
	TxDirectDeposits          map[cbor.ByteString]uint64                    `cbor:"25,keyasint,omitempty"`
	TxAccountBalanceIntervals *DijkstraRawCbor                              `cbor:"26,keyasint,omitempty"`
}

func (b *DijkstraSubTransactionBody) UnmarshalCBOR(cborData []byte) error {
	type tDijkstraSubTransactionBody DijkstraSubTransactionBody
	var tmp tDijkstraSubTransactionBody
	if _, err := cbor.Decode(cborData, &tmp); err != nil {
		return err
	}
	*b = DijkstraSubTransactionBody(tmp)
	b.SetCborReference(cborData)
	return nil
}

func (b *DijkstraSubTransactionBody) Inputs() []common.TransactionInput {
	return dijkstraTransactionInputs(b.TxInputs.Items())
}

func (b *DijkstraSubTransactionBody) Outputs() []common.TransactionOutput {
	return dijkstraTransactionOutputs(b.TxOutputs)
}

func (b *DijkstraSubTransactionBody) TTL() uint64 {
	return b.Ttl
}

func (b *DijkstraSubTransactionBody) ValidityIntervalStart() uint64 {
	return b.TxValidityIntervalStart
}

func (b *DijkstraSubTransactionBody) ProtocolParameterUpdates() (uint64, map[common.Blake2b224]common.ProtocolParameterUpdate) {
	return 0, nil
}

func (b *DijkstraSubTransactionBody) Certificates() []common.Certificate {
	return dijkstraCertificates(b.TxCertificates)
}

func (b *DijkstraSubTransactionBody) Withdrawals() map[*common.Address]*big.Int {
	return dijkstraWithdrawals(b.TxWithdrawals)
}

func (b *DijkstraSubTransactionBody) AuxDataHash() *common.Blake2b256 {
	return b.TxAuxDataHash
}

func (b *DijkstraSubTransactionBody) AssetMint() *common.MultiAsset[common.MultiAssetTypeMint] {
	return b.TxMint
}

func (b *DijkstraSubTransactionBody) RequiredSigners() []common.Blake2b224 {
	return dijkstraRequiredSigners(b.TxGuards)
}

func (b *DijkstraSubTransactionBody) ScriptDataHash() *common.Blake2b256 {
	return b.TxScriptDataHash
}

func (b *DijkstraSubTransactionBody) ReferenceInputs() []common.TransactionInput {
	return dijkstraReferenceInputs(b.TxReferenceInputs)
}

func (b *DijkstraSubTransactionBody) VotingProcedures() common.VotingProcedures {
	return b.TxVotingProcedures
}

func (b *DijkstraSubTransactionBody) ProposalProcedures() []common.ProposalProcedure {
	return dijkstraProposalProcedures(b.TxProposalProcedures)
}

func (b *DijkstraSubTransactionBody) CurrentTreasuryValue() *big.Int {
	return new(big.Int).SetUint64(b.TxCurrentTreasuryValue)
}

func (b *DijkstraSubTransactionBody) Donation() *big.Int {
	return new(big.Int).SetUint64(b.TxDonation)
}

func (b *DijkstraSubTransactionBody) Utxorpc() (*utxorpc.Tx, error) {
	return common.TransactionBodyToUtxorpc(b)
}

type DijkstraRedeemers struct {
	cbor.DecodeStoreCbor
	Redeemers map[common.RedeemerKey]common.RedeemerValue
}

func (r *DijkstraRedeemers) UnmarshalCBOR(cborData []byte) error {
	if len(cborData) == 0 || (cborData[0]&cbor.CborTypeMask) != cbor.CborTypeMap {
		return errors.New("dijkstra redeemers must use map encoding")
	}
	var redeemers map[common.RedeemerKey]common.RedeemerValue
	if _, err := cbor.Decode(cborData, &redeemers); err != nil {
		return err
	}
	if len(redeemers) == 0 {
		return errors.New("dijkstra redeemers must not be empty")
	}
	r.Redeemers = redeemers
	r.SetCbor(cborData)
	return nil
}

func (r DijkstraRedeemers) MarshalCBOR() ([]byte, error) {
	if raw := r.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	return cbor.Encode(r.Redeemers)
}

func (r DijkstraRedeemers) Len() int {
	return len(r.Redeemers)
}

func (r DijkstraRedeemers) Iter() iter.Seq2[common.RedeemerKey, common.RedeemerValue] {
	return func(yield func(common.RedeemerKey, common.RedeemerValue) bool) {
		sorted := slices.Collect(maps.Keys(r.Redeemers))
		slices.SortFunc(sorted, common.CompareRedeemerKeys)
		for _, redeemerKey := range sorted {
			tmpVal := r.Redeemers[redeemerKey]
			if !yield(redeemerKey, tmpVal) {
				return
			}
		}
	}
}

func (r DijkstraRedeemers) Indexes(tag common.RedeemerTag) []uint {
	ret := []uint{}
	for key := range r.Redeemers {
		if key.Tag == tag {
			ret = append(ret, uint(key.Index))
		}
	}
	return ret
}

func (r DijkstraRedeemers) Value(index uint, tag common.RedeemerTag) common.RedeemerValue {
	redeemerVal, ok := r.Redeemers[common.RedeemerKey{
		Tag:   tag,
		Index: uint32(index), // #nosec G115
	}]
	if ok {
		return redeemerVal
	}
	return common.RedeemerValue{}
}

type DijkstraTransactionWitnessSet struct {
	cbor.DecodeStoreCbor
	VkeyWitnesses      cbor.SetType[common.VkeyWitness]      `cbor:"0,keyasint,omitempty,omitzero"`
	WsNativeScripts    cbor.SetType[common.NativeScript]     `cbor:"1,keyasint,omitempty,omitzero"`
	BootstrapWitnesses cbor.SetType[common.BootstrapWitness] `cbor:"2,keyasint,omitempty,omitzero"`
	WsPlutusV1Scripts  cbor.SetType[common.PlutusV1Script]   `cbor:"3,keyasint,omitempty,omitzero"`
	WsPlutusData       cbor.SetType[common.Datum]            `cbor:"4,keyasint,omitempty,omitzero"`
	WsRedeemers        DijkstraRedeemers                     `cbor:"5,keyasint,omitempty,omitzero"`
	WsPlutusV2Scripts  cbor.SetType[common.PlutusV2Script]   `cbor:"6,keyasint,omitempty,omitzero"`
	WsPlutusV3Scripts  cbor.SetType[common.PlutusV3Script]   `cbor:"7,keyasint,omitempty,omitzero"`
	WsPlutusV4Scripts  cbor.SetType[common.PlutusV4Script]   `cbor:"8,keyasint,omitempty,omitzero"`
}

func (w *DijkstraTransactionWitnessSet) UnmarshalCBOR(cborData []byte) error {
	type tDijkstraTransactionWitnessSet DijkstraTransactionWitnessSet
	var tmp tDijkstraTransactionWitnessSet
	if _, err := cbor.Decode(cborData, &tmp); err != nil {
		return err
	}
	*w = DijkstraTransactionWitnessSet(tmp)
	w.SetCbor(cborData)
	return nil
}

func (w DijkstraTransactionWitnessSet) Vkey() []common.VkeyWitness {
	return w.VkeyWitnesses.Items()
}

func (w DijkstraTransactionWitnessSet) Bootstrap() []common.BootstrapWitness {
	return w.BootstrapWitnesses.Items()
}

func (w DijkstraTransactionWitnessSet) NativeScripts() []common.NativeScript {
	return w.WsNativeScripts.Items()
}

func (w DijkstraTransactionWitnessSet) PlutusV1Scripts() []common.PlutusV1Script {
	return w.WsPlutusV1Scripts.Items()
}

func (w DijkstraTransactionWitnessSet) PlutusV2Scripts() []common.PlutusV2Script {
	return w.WsPlutusV2Scripts.Items()
}

func (w DijkstraTransactionWitnessSet) PlutusV3Scripts() []common.PlutusV3Script {
	return w.WsPlutusV3Scripts.Items()
}

func (w DijkstraTransactionWitnessSet) PlutusV4Scripts() []common.PlutusV4Script {
	return w.WsPlutusV4Scripts.Items()
}

func (w DijkstraTransactionWitnessSet) PlutusData() []common.Datum {
	return w.WsPlutusData.Items()
}

func (w DijkstraTransactionWitnessSet) Redeemers() common.TransactionWitnessRedeemers {
	return w.WsRedeemers
}

type DijkstraTransaction struct {
	cbor.StructAsArray
	cbor.DecodeStoreCbor
	hash       *common.Blake2b256
	Body       DijkstraTransactionBody
	WitnessSet DijkstraTransactionWitnessSet
	TxIsValid  bool
	TxMetadata common.TransactionMetadatum
	auxData    common.AuxiliaryData
}

func (t *DijkstraTransaction) UnmarshalCBOR(cborData []byte) error {
	tmpTx, err := newDijkstraTransactionFromCbor(cborData, true)
	if err != nil {
		return err
	}
	*t = *tmpTx
	return nil
}

func (t *DijkstraTransaction) Metadata() common.TransactionMetadatum {
	return t.TxMetadata
}

func (t *DijkstraTransaction) AuxiliaryData() common.AuxiliaryData {
	return t.auxData
}

func (DijkstraTransaction) Type() int {
	return TxTypeDijkstra
}

func (t DijkstraTransaction) Hash() common.Blake2b256 {
	return t.Id()
}

func (t DijkstraTransaction) Id() common.Blake2b256 {
	return t.Body.Id()
}

func (t *DijkstraTransaction) LeiosHash() common.Blake2b256 {
	if t.hash == nil {
		tmpHash := common.Blake2b256Hash(t.Cbor())
		t.hash = &tmpHash
	}
	return *t.hash
}

func (t DijkstraTransaction) Inputs() []common.TransactionInput {
	return t.Body.Inputs()
}

func (t DijkstraTransaction) Outputs() []common.TransactionOutput {
	return t.Body.Outputs()
}

func (t DijkstraTransaction) Fee() *big.Int {
	return t.Body.Fee()
}

func (t DijkstraTransaction) TTL() uint64 {
	return t.Body.TTL()
}

func (t DijkstraTransaction) ValidityIntervalStart() uint64 {
	return t.Body.ValidityIntervalStart()
}

func (t DijkstraTransaction) ProtocolParameterUpdates() (uint64, map[common.Blake2b224]common.ProtocolParameterUpdate) {
	return t.Body.ProtocolParameterUpdates()
}

func (t DijkstraTransaction) ReferenceInputs() []common.TransactionInput {
	return t.Body.ReferenceInputs()
}

func (t DijkstraTransaction) Collateral() []common.TransactionInput {
	return t.Body.Collateral()
}

func (t DijkstraTransaction) CollateralReturn() common.TransactionOutput {
	return t.Body.CollateralReturn()
}

func (t DijkstraTransaction) TotalCollateral() *big.Int {
	return t.Body.TotalCollateral()
}

func (t DijkstraTransaction) Certificates() []common.Certificate {
	return t.Body.Certificates()
}

func (t DijkstraTransaction) Withdrawals() map[*common.Address]*big.Int {
	return t.Body.Withdrawals()
}

func (t DijkstraTransaction) AuxDataHash() *common.Blake2b256 {
	return t.Body.AuxDataHash()
}

func (t DijkstraTransaction) RequiredSigners() []common.Blake2b224 {
	return t.Body.RequiredSigners()
}

func (t DijkstraTransaction) AssetMint() *common.MultiAsset[common.MultiAssetTypeMint] {
	return t.Body.AssetMint()
}

func (t DijkstraTransaction) ScriptDataHash() *common.Blake2b256 {
	return t.Body.ScriptDataHash()
}

func (t DijkstraTransaction) VotingProcedures() common.VotingProcedures {
	return t.Body.VotingProcedures()
}

func (t DijkstraTransaction) ProposalProcedures() []common.ProposalProcedure {
	return t.Body.ProposalProcedures()
}

func (t DijkstraTransaction) CurrentTreasuryValue() *big.Int {
	return t.Body.CurrentTreasuryValue()
}

func (t DijkstraTransaction) Donation() *big.Int {
	return t.Body.Donation()
}

func (t DijkstraTransaction) NetworkId() *uint8 {
	return t.Body.NetworkId()
}

func (t DijkstraTransaction) IsValid() bool {
	return t.TxIsValid
}

func (t DijkstraTransaction) Consumed() []common.TransactionInput {
	if t.IsValid() {
		return t.Inputs()
	}
	return t.Collateral()
}

func (t DijkstraTransaction) Produced() []common.Utxo {
	if t.IsValid() {
		outputs := t.Outputs()
		ret := make([]common.Utxo, 0, len(outputs))
		for idx, output := range outputs {
			ret = append(
				ret,
				common.Utxo{
					Id: shelley.NewShelleyTransactionInput(
						t.Hash().String(),
						idx,
					),
					Output: output,
				},
			)
		}
		return ret
	}
	if t.CollateralReturn() == nil {
		return []common.Utxo{}
	}
	return []common.Utxo{
		{
			Id: shelley.NewShelleyTransactionInput(
				t.Hash().String(),
				len(t.Outputs()),
			),
			Output: t.CollateralReturn(),
		},
	}
}

func (t DijkstraTransaction) Witnesses() common.TransactionWitnessSet {
	return t.WitnessSet
}

func (t *DijkstraTransaction) MarshalCBOR() ([]byte, error) {
	if cborData := t.DecodeStoreCbor.Cbor(); cborData != nil {
		return cborData, nil
	}
	var aux any
	if t.auxData != nil && len(t.auxData.Cbor()) > 0 {
		aux = cbor.RawMessage(t.auxData.Cbor())
	} else if t.TxMetadata != nil {
		aux = cbor.RawMessage(t.TxMetadata.Cbor())
	}
	return cbor.Encode([]any{t.Body, t.WitnessSet, aux})
}

// blockTransactionCbor returns the transaction in the Leios prototype
// (Musashi) block form, [body, witness_set, auxiliary_data / nil, is_valid].
func (t *DijkstraTransaction) blockTransactionCbor() ([]byte, error) {
	if raw := t.DecodeStoreCbor.Cbor(); raw != nil {
		var txArray []cbor.RawMessage
		if _, err := cbor.Decode(raw, &txArray); err == nil &&
			len(txArray) == 4 && isCborBool(txArray[3]) {
			return raw, nil
		}
	}
	var aux any
	if t.auxData != nil && len(t.auxData.Cbor()) > 0 {
		aux = cbor.RawMessage(t.auxData.Cbor())
	} else if t.TxMetadata != nil {
		aux = cbor.RawMessage(t.TxMetadata.Cbor())
	}
	return cbor.Encode([]any{t.Body, t.WitnessSet, aux, t.TxIsValid})
}

func (t *DijkstraTransaction) Cbor() []byte {
	if cborData := t.DecodeStoreCbor.Cbor(); cborData != nil {
		return cborData[:]
	}
	if t.Body.Cbor() == nil {
		return nil
	}
	cborData, err := t.MarshalCBOR()
	if err != nil {
		panic("CBOR encoding that should never fail has failed: " + err.Error())
	}
	return cborData
}

func (t *DijkstraTransaction) Utxorpc() (*utxorpc.Tx, error) {
	tx, err := t.Body.Utxorpc()
	if err != nil {
		return nil, fmt.Errorf("failed to convert Dijkstra transaction: %w", err)
	}
	return tx, nil
}

type DijkstraSubTransaction struct {
	cbor.StructAsArray
	cbor.DecodeStoreCbor
	Body       DijkstraSubTransactionBody
	WitnessSet DijkstraTransactionWitnessSet
	TxMetadata common.TransactionMetadatum
	auxData    common.AuxiliaryData
}

func (t *DijkstraSubTransaction) UnmarshalCBOR(cborData []byte) error {
	var txArray []cbor.RawMessage
	if _, err := cbor.Decode(cborData, &txArray); err != nil {
		return err
	}
	if len(txArray) != 3 {
		return fmt.Errorf(
			"invalid Dijkstra sub-transaction: expected 3 components, got %d",
			len(txArray),
		)
	}
	if _, err := cbor.Decode(txArray[0], &t.Body); err != nil {
		return fmt.Errorf("failed to decode sub-transaction body: %w", err)
	}
	if _, err := cbor.Decode(txArray[1], &t.WitnessSet); err != nil {
		return fmt.Errorf(
			"failed to decode sub-transaction witness set: %w",
			err,
		)
	}
	if err := decodeAuxiliaryDataInto(txArray[2], &t.TxMetadata, &t.auxData); err != nil {
		return err
	}
	t.SetCbor(cborData)
	return nil
}

func (t *DijkstraSubTransaction) MarshalCBOR() ([]byte, error) {
	if raw := t.Cbor(); len(raw) > 0 {
		return raw, nil
	}
	var aux any
	if t.auxData != nil && len(t.auxData.Cbor()) > 0 {
		aux = cbor.RawMessage(t.auxData.Cbor())
	} else if t.TxMetadata != nil {
		aux = cbor.RawMessage(t.TxMetadata.Cbor())
	}
	return cbor.Encode([]any{t.Body, t.WitnessSet, aux})
}

func NewDijkstraBlockFromCbor(
	data []byte,
	config ...common.VerifyConfig,
) (*DijkstraBlock, error) {
	var cfg common.VerifyConfig
	if len(config) > 0 {
		cfg = config[0]
	}
	var dijkstraBlock DijkstraBlock
	if _, err := cbor.Decode(data, &dijkstraBlock); err != nil {
		return nil, fmt.Errorf("decode Dijkstra block error: %w", err)
	}
	if !cfg.SkipBodyHashValidation {
		if dijkstraBlock.BlockHeader == nil {
			return nil, errors.New("dijkstra block header is nil")
		}
		expected := dijkstraBlock.BlockHeader.BlockBodyHash()
		actual := dijkstraBlock.CalculatedBlockBodyHash()
		actualBytes := actual[:]
		expectedBytes := expected[:]
		if len(actualBytes) != len(expectedBytes) ||
			subtle.ConstantTimeCompare(actualBytes, expectedBytes) != 1 {
			return nil, common.NewValidationError(
				common.ValidationErrorTypeBodyHash,
				"Dijkstra block body hash mismatch during parsing",
				map[string]any{
					"era":           EraNameDijkstra,
					"expected_hash": expected.String(),
					"actual_hash":   actual.String(),
				},
				nil,
			)
		}
	}
	return &dijkstraBlock, nil
}

func NewDijkstraBlockHeaderFromCbor(data []byte) (*DijkstraBlockHeader, error) {
	var dijkstraBlockHeader DijkstraBlockHeader
	if _, err := cbor.Decode(data, &dijkstraBlockHeader); err != nil {
		return nil, fmt.Errorf("decode Dijkstra block header error: %w", err)
	}
	return &dijkstraBlockHeader, nil
}

func NewDijkstraTransactionBodyFromCbor(
	data []byte,
) (*DijkstraTransactionBody, error) {
	var dijkstraTx DijkstraTransactionBody
	if _, err := cbor.Decode(data, &dijkstraTx); err != nil {
		return nil, fmt.Errorf("decode Dijkstra transaction body error: %w", err)
	}
	return &dijkstraTx, nil
}

func NewDijkstraTransactionFromCbor(data []byte) (*DijkstraTransaction, error) {
	return newDijkstraTransactionFromCbor(data, true)
}

// NewDijkstraTransactionFromCborComponents decodes a Dijkstra transaction
// from an already-decoded top-level transaction array.
func NewDijkstraTransactionFromCborComponents(
	data []byte,
	txArray []cbor.RawMessage,
) (*DijkstraTransaction, error) {
	if err := validateDijkstraTransactionCborSize(data); err != nil {
		return nil, err
	}
	return newDijkstraTransactionFromCborComponents(data, txArray, true)
}

func newDijkstraTransactionFromCbor(
	data []byte,
	allowIsValid bool,
) (*DijkstraTransaction, error) {
	if err := validateDijkstraTransactionCborSize(data); err != nil {
		return nil, err
	}
	var txArray []cbor.RawMessage
	if _, err := cbor.Decode(data, &txArray); err != nil {
		return nil, err
	}
	return newDijkstraTransactionFromCborComponents(data, txArray, allowIsValid)
}

func validateDijkstraTransactionCborSize(data []byte) error {
	if len(data) <= MaxTxSize {
		return nil
	}
	return fmt.Errorf(
		"newDijkstraTransactionFromCbor: transaction size %d exceeds MaxTxSize %d",
		len(data),
		MaxTxSize,
	)
}

func newDijkstraTransactionFromCborComponents(
	data []byte,
	txArray []cbor.RawMessage,
	allowIsValid bool,
) (*DijkstraTransaction, error) {
	var ret DijkstraTransaction
	if len(txArray) != 3 && len(txArray) != 4 {
		return nil, fmt.Errorf(
			"invalid Dijkstra transaction: expected 3 or 4 components, got %d",
			len(txArray),
		)
	}
	if len(txArray) == 4 && !allowIsValid {
		return nil, errors.New(
			"dijkstra transactions in blocks cannot include is_valid",
		)
	}
	if _, err := cbor.Decode(txArray[0], &ret.Body); err != nil {
		return nil, fmt.Errorf("failed to decode transaction body: %w", err)
	}
	if _, err := cbor.Decode(txArray[1], &ret.WitnessSet); err != nil {
		return nil, fmt.Errorf("failed to decode transaction witness set: %w", err)
	}
	auxIdx := 2
	ret.TxIsValid = true
	switch {
	case len(txArray) == 4 && isCborBool(txArray[3]):
		// Leios prototype (Musashi) block_transaction:
		// [transaction_body, transaction_witness_set, auxiliary_data / nil, is_valid]
		if _, err := cbor.Decode(txArray[3], &ret.TxIsValid); err != nil {
			return nil, fmt.Errorf("failed to decode TxIsValid: %w", err)
		}
	case len(txArray) == 4:
		var txIsValid bool
		if _, err := cbor.Decode(txArray[2], &txIsValid); err != nil {
			return nil, fmt.Errorf("failed to decode TxIsValid: %w", err)
		}
		if !txIsValid {
			return nil, errors.New("dijkstra transactions cannot encode is_valid=false")
		}
		auxIdx = 3
	}
	if err := decodeAuxiliaryDataInto(
		txArray[auxIdx],
		&ret.TxMetadata,
		&ret.auxData,
	); err != nil {
		return nil, err
	}
	ret.SetCbor(data)
	return &ret, nil
}

func decodeInvalidTransactions(raw cbor.RawMessage) ([]uint, error) {
	if isCborNull(raw) {
		return nil, nil
	}
	var txIndices []uint
	if _, err := cbor.Decode(raw, &txIndices); err != nil {
		return nil, fmt.Errorf("decode Dijkstra invalid transactions: %w", err)
	}
	return txIndices, nil
}

func decodeAuxiliaryDataInto(
	raw cbor.RawMessage,
	metadata *common.TransactionMetadatum,
	auxData *common.AuxiliaryData,
) error {
	*metadata = nil
	*auxData = nil
	if isCborNull(raw) {
		return nil
	}
	aux, err := common.DecodeAuxiliaryData(raw)
	if err == nil && aux != nil {
		*auxData = aux
		md, _ := aux.Metadata()
		if md != nil {
			*metadata = md
		}
		return nil
	}
	md, err := common.DecodeAuxiliaryDataToMetadata(raw)
	if err == nil && md != nil {
		*metadata = md
		return nil
	}
	return errors.New("decode Dijkstra auxiliary data")
}

func isCborNull(raw cbor.RawMessage) bool {
	return len(raw) == 1 && raw[0] == 0xf6
}

func isCborBool(raw cbor.RawMessage) bool {
	return len(raw) == 1 && (raw[0] == 0xf4 || raw[0] == 0xf5)
}
