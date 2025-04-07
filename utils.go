package taponark

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

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

func ExtractColoredTransfer(btcPacket *psbt.Packet, transferOutput *tappsbt.VOutput) (ColoredTransfer, error) {
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
		return ColoredTransfer{}, fmt.Errorf("cannot encode tapscript preimage %v", err)
	}

	finalTx, err := psbt.Extract(btcPacket)
	if err != nil {
		return ColoredTransfer{}, fmt.Errorf("cannot extract final transaction %v", err)
	}
	txhash := transferOutput.ProofSuffix.AnchorTx.TxHash()

	outpoint := wire.NewOutPoint(&txhash, transferOutput.AnchorOutputIndex)

	anchorValue := btcPacket.UnsignedTx.TxOut[transferOutput.AnchorOutputIndex].Value
	assetAmount := transferOutput.Amount

	return ColoredTransfer{finalTx, outpoint, transferOutput.ProofSuffix, merkleRoot, taprootSibling, internalKey, scriptKey, anchorValue, taprootAssetRoot, assetAmount}, nil

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

func addBtcOutput(transferPacket *psbt.Packet, amount uint64, internalKey *btcec.PublicKey) error {

	taprootKey := txscript.ComputeTaprootOutputKey(internalKey, []byte{})

	pkscript, err := txscript.PayToTaprootScript(taprootKey)

	if err != nil {
		return fmt.Errorf("cannot convert address to script %v", err)
	}

	txout := wire.TxOut{
		Value:    int64(amount),
		PkScript: pkscript,
	}

	transferPacket.UnsignedTx.TxOut = append(
		transferPacket.UnsignedTx.TxOut, &txout,
	)

	transferPacket.Outputs = append(transferPacket.Outputs, psbt.POutput{
		TaprootInternalKey: schnorr.SerializePubKey(internalKey),
		TaprootTapTree:     nil,
	})

	return nil
}

// waitForTransfers concurrently waits for both the BTC confirmation and the asset transfer event.
func waitForTransfers(bitcoinClient *BitcoinClient, serverTapClient *TapClient, txHash chainhash.Hash, assetAddr *taprpc.Addr) error {
	var wg sync.WaitGroup
	errCh := make(chan error, 2)

	// Wait for BTC confirmation concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := bitcoinClient.WaitForConfirmation(txHash); err != nil {
			errCh <- fmt.Errorf("BTC confirmation failed: %w", err)
		}
	}()

	// Wait for the asset transfer event concurrently.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := serverTapClient.IncomingTransferEvent(assetAddr); err != nil {
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

// RandomHexString generates a random hexadecimal string of length n*2.
func RandomHexString(n int) (string, error) {
	// n bytes will result in n*2 hex characters.
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
