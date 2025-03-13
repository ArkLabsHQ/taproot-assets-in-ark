package taponark

import (
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/tappsbt"
)

const BOARDING_TRANSFER_OUTPUT_INDEX = 1
const ASSET_BOARDING_INPUT_INDEX = 0
const BTC_BOARDING_INPUT_INDEX = 1
const TRANSFER_INPUT_INDEX = 0
const LEFT_TRANSFER_OUTPUT_INDEX = 1
const RIGHT_TRANSFER_OUTPUT_INDEX = 2
const CHANGE_OUTPUT_INDEX = 0

func DeriveUnpublishedChainTransfer(btcPacket *psbt.Packet, transferOutput *tappsbt.VOutput) ChainTransfer {
	internalKey := transferOutput.AnchorOutputInternalKey
	scriptKey := transferOutput.ScriptKey
	merkleRoot := tappsbt.ExtractCustomField(
		btcPacket.Outputs[transferOutput.AnchorOutputIndex].Unknowns, tappsbt.PsbtKeyTypeOutputTaprootMerkleRoot,
	)
	taprootAssetRoot := tappsbt.ExtractCustomField(
		btcPacket.Outputs[transferOutput.AnchorOutputIndex].Unknowns, tappsbt.PsbtKeyTypeOutputAssetRoot,
	)
	taprootSibling, _, err := commitment.MaybeEncodeTapscriptPreimage(transferOutput.AnchorOutputTapscriptSibling)
	if err != nil {
		log.Fatalf("cannot encode tapscript preimage %v", err)
	}

	finalTx, err := psbt.Extract(btcPacket)
	if err != nil {
		log.Fatalf("cannot extract final transaction %v", err)
	}
	txhash := transferOutput.ProofSuffix.AnchorTx.TxHash()

	outpoint := wire.NewOutPoint(&txhash, transferOutput.AnchorOutputIndex)

	anchorValue := btcPacket.UnsignedTx.TxOut[transferOutput.AnchorOutputIndex].Value
	assetAmount := transferOutput.Amount

	return ChainTransfer{finalTx, outpoint, transferOutput.ProofSuffix, merkleRoot, taprootSibling, internalKey, scriptKey, anchorValue, taprootAssetRoot, assetAmount}

}

func extractControlBlock(arkBtcScript ArkBtcScript, taprootAssetRoot []byte) *txscript.ControlBlock {
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}

	rightNodeHash := arkBtcScript.unilateralSpend.TapHash()
	inclusionproof := append(rightNodeHash[:], taprootAssetRoot[:]...)
	btcControlBlock.InclusionProof = inclusionproof
	rootHash := btcControlBlock.RootHash(arkBtcScript.cooperativeSpend.Script)
	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		btcControlBlock.OutputKeyYIsOdd = true
	}

	return btcControlBlock
}

func addBtcInput(transferPacket *psbt.Packet, btcTransferDetails BtcTransferDetails) {
	signingDetails := btcTransferDetails

	transferPacket.UnsignedTx.TxIn = append(
		transferPacket.UnsignedTx.TxIn, &wire.TxIn{
			PreviousOutPoint: *signingDetails.outpoint,
		},
	)

	controlBlock := signingDetails.arkSpendingDetails.arkBtcScript.controlBlock
	rootHash := controlBlock.RootHash(signingDetails.arkSpendingDetails.arkBtcScript.cooperativeSpend.Script)
	transferPacket.Inputs = append(transferPacket.Inputs, psbt.PInput{
		WitnessUtxo: signingDetails.txout,
		TaprootInternalKey: schnorr.SerializePubKey(
			asset.NUMSPubKey,
		),
		TaprootMerkleRoot: rootHash,
	})

}

func addBtcOutput(transferPacket *psbt.Packet, amount uint64, taprootKey *btcec.PublicKey, rawInternalKey []byte) {
	pkscript, err := txscript.PayToTaprootScript(taprootKey)

	if err != nil {
		log.Fatalf("cannot convert address to script %v", err)
	}

	txout := wire.TxOut{
		Value:    int64(amount),
		PkScript: pkscript,
	}

	transferPacket.UnsignedTx.TxOut = append(
		transferPacket.UnsignedTx.TxOut, &txout,
	)

	transferPacket.Outputs = append(transferPacket.Outputs, psbt.POutput{
		TaprootInternalKey: rawInternalKey,
		TaprootTapTree:     nil,
	})

}
