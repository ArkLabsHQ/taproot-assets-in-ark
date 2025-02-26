package taponark

import (
	"log"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/tappsbt"
)

const CHANGE_OUTPUT_INDEX = 0
const TRANSFER_INPUT_INDEX = 0
const TRANSFER_OUTPUT_INDEX = 1

func DeriveUnpublishedChainTransfer(btcPacket *psbt.Packet, transferPacket *tappsbt.VPacket) ChainTransfer {
	transferOutput := transferPacket.Outputs[TRANSFER_OUTPUT_INDEX]
	changeOutput := transferPacket.Outputs[CHANGE_OUTPUT_INDEX]
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

	return ChainTransfer{finalTx, outpoint, transferOutput.ProofSuffix, changeOutput.ProofSuffix, merkleRoot, taprootSibling, internalKey, scriptKey, anchorValue, taprootAssetRoot}

}

func extractControlBlock(arkScript ArkScript, taprootAssetRoot []byte) *txscript.ControlBlock {
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}

	rightNodeHash := arkScript.unilateralSpend.TapHash()
	inclusionproof := append(rightNodeHash[:], taprootAssetRoot[:]...)
	btcControlBlock.InclusionProof = inclusionproof
	rootHash := btcControlBlock.RootHash(arkScript.cooperativeSpend.Script)
	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		btcControlBlock.OutputKeyYIsOdd = true
	}

	return btcControlBlock
}
