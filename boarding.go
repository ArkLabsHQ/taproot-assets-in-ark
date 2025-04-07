package taponark

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/lightninglabs/taproot-assets/asset"
)

// / Logic To Onboard User ( BTC + ASSET)
func OnboardUser(assetId []byte, boardingAssetAmount uint64, boardingBtcAmount uint64, boardingClient, serverTapClient *TapClient, bitcoinClient *BitcoinClient) (ArkBoardingTransfer, error) {
	/// 1. Send Asset From Boarding User To Boarding Address
	// Create Server Boarding Details
	assetSpendingDetails, err := CreateOnboardSpendingDetails(boardingClient, serverTapClient)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot create Boarding Spending Details %v", err)
	}

	// Deriver Server Boarding Address
	boardingAddrResp, err := serverTapClient.GetNewAddress(assetSpendingDetails.arkBtcScript.Branch, assetSpendingDetails.arkAssetScript.tapScriptKey, assetId, boardingAssetAmount)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot get Boarding address from Server %v", err)
	}

	// Send Asset to onboarding address
	sendBoardingAssetResp, err := boardingClient.SendAsset(boardingAddrResp)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("failed to send boarding asset [%s] to boarding address %v", hex.EncodeToString(assetId), err)
	}

	log.Printf("Boarding Asset TxId %s", hex.EncodeToString(sendBoardingAssetResp.Transfer.AnchorTxHash))

	// Insert Boarding AssetSpendingDetails Control Block
	assetTransferOutput := sendBoardingAssetResp.Transfer.Outputs[BOARDING_ASSET_TRANSFER_OUTPUT_INDEX]
	taprootAssetRoot := assetTransferOutput.Anchor.TaprootAssetRoot
	assetControlBlock := extractControlBlock(assetSpendingDetails.arkBtcScript, taprootAssetRoot)
	assetSpendingDetails.arkBtcScript.controlBlock = assetControlBlock

	/// 2. Send BTC From Boarding User To Boarding Address
	zeroHash := taprootAssetRoot
	btcSpendingDetails, err := CreateOnboardSpendingDetails(boardingClient, serverTapClient)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot create boarding spending details %v", err)
	}

	// Create Boarding BTC OutputScript
	btcControlBlock := extractControlBlock(btcSpendingDetails.arkBtcScript, zeroHash)
	btcSpendingDetails.arkBtcScript.controlBlock = btcControlBlock
	rootHash := btcControlBlock.RootHash(btcSpendingDetails.arkBtcScript.cooperativeSpend.Script)
	outputKey := txscript.ComputeTaprootOutputKey(asset.NUMSPubKey, rootHash)
	pkScript, err := txscript.PayToTaprootScript(outputKey)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot create Btc Boarding Output Script %v", err)
	}

	// Send BTC to Onboarding Output
	onboardBtcTransaction, err := boardingClient.lndClient.SendOutput(int64(boardingBtcAmount), pkScript)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot send btc to output %v", err)
	}

	log.Printf("Boarding BTC TxId %s", onboardBtcTransaction.TxHash().String())

	var (
		matchedTxOut    *wire.TxOut
		matchedOutPoint *wire.OutPoint
		txHash          = onboardBtcTransaction.TxHash()
	)

	for idx, output := range onboardBtcTransaction.TxOut {
		if bytes.Equal(output.PkScript, pkScript) {
			matchedTxOut = output
			matchedOutPoint = &wire.OutPoint{
				Hash:  txHash,
				Index: uint32(idx),
			}
			break
		}
	}

	if matchedTxOut == nil || matchedOutPoint == nil {
		return ArkBoardingTransfer{}, fmt.Errorf("unable to locate matching TxOut and OutPoint")
	}

	boardingBtcTransferDetails := BtcTransferDetails{
		matchedTxOut, matchedOutPoint, boardingBtcAmount, btcSpendingDetails,
	}

	// Ensure Btc and Asset Transfer Sucess
	waitForTransfers(bitcoinClient, serverTapClient, onboardBtcTransaction.TxHash(), boardingAddrResp)

	// Fetch Onboard Asset Transfer Proof
	assetTransferProof, err := serverTapClient.ExportProof(assetId,
		assetTransferOutput.ScriptKey,
	)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot fetch Boarding Transfer Proof %v", err)
	}
	assetTransferDetails := AssetTransferDetails{assetTransferOutput, assetSpendingDetails, boardingAssetAmount, assetTransferProof}

	return ArkBoardingTransfer{assetTransferDetails, boardingBtcTransferDetails, boardingClient}, nil
}
