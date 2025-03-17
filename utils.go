package taponark

import (
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
)

const BOARDING_ASSET_TRANSFER_OUTPUT_INDEX = 1
const ASSET_ANCHOR_ROUND_ROOT_INPUT_INDEX = 0

const CHANGE_OUTPUT_INDEX = 0

const FEE uint64 = 10_000
const DUMMY_ASSET_BTC_AMOUNT = 1_000
const ROUND_ROOT_ANCHOR_OUTPUT_INDEX = 0
const ROUND_ROOT_ASSET_OUTPUT_INDEX = 0

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

func addBtcInputToPSBT(transferPacket *psbt.Packet, btcTransferDetails BtcTransferDetails) {
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

// waitForTransfers concurrently waits for both the BTC confirmation and the asset transfer event.
func waitForTransfers(bitcoinClient *BitcoinClient, serverTapClient *TapClient, txHash chainhash.Hash, assetAddr *taprpc.Addr, timeout time.Duration) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	// Wait for BTC confirmation concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := bitcoinClient.WaitForConfirmation(txHash, timeout); err != nil {
			errCh <- fmt.Errorf("BTC confirmation failed: %w", err)
		}
	}()

	// Wait for the asset transfer event concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := serverTapClient.IncomingTransferEvent(assetAddr, timeout); err != nil {
			errCh <- fmt.Errorf("asset transfer event failed: %w", err)
		}
	}()

	// Wait for both routines to finish.
	wg.Wait()
	close(errCh)

	// If any error occurred, return the first one encountered.
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}
