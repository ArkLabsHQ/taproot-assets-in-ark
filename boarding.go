package taponark

import (
	"bytes"
	"context"
	"fmt"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/tapsend"
	"github.com/lightningnetwork/lnd/keychain"
)

// user tap client

func OnboardUser(assetId []byte, asset_amnt uint64, btc_amnt uint64, boardingClient, serverTapClient *TapClient, bitcoinClient *BitcoinClient) (ArkBoardingTransfer, error) {

	/// 1. Send Asset From  Boarding User To Boarding Address
	assetSpendingDetails, err := CreateBoardingSpendingDetails(boardingClient, serverTapClient)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot create Boarding Spending Details %v", err)
	}

	addr_resp, err := serverTapClient.GetNewAddress(assetSpendingDetails.arkBtcScript.Branch, assetSpendingDetails.arkAssetScript.tapScriptKey, assetId, asset_amnt)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot get address %v", err)
	}

	// sent asset to onboarding address
	sendResp, err := boardingClient.SendAsset(addr_resp)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot send asset %v", err)
	}

	// Insert Control Block
	assetTransferOutput := sendResp.Transfer.Outputs[BOARDING_ASSET_TRANSFER_OUTPUT_INDEX]
	taprootAssetRoot := assetTransferOutput.Anchor.TaprootAssetRoot
	assetControlBlock := extractControlBlock(assetSpendingDetails.arkBtcScript, taprootAssetRoot)
	assetSpendingDetails.arkBtcScript.controlBlock = assetControlBlock

	/// 2. Send BTC From Boarding User To Boarding Address
	zeroHash := taprootAssetRoot
	btcSpendingDetails, err := CreateBoardingSpendingDetails(boardingClient, serverTapClient)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot create boarding spending details %v", err)
	}

	btcControlBlock := extractControlBlock(btcSpendingDetails.arkBtcScript, zeroHash)
	btcSpendingDetails.arkBtcScript.controlBlock = btcControlBlock
	rootHash := btcControlBlock.RootHash(btcSpendingDetails.arkBtcScript.cooperativeSpend.Script)
	outputKey := txscript.ComputeTaprootOutputKey(asset.NUMSPubKey, rootHash)
	pkScript, err := txscript.PayToTaprootScript(outputKey)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot create Pay To Taproot Script", err)
	}

	//send Btc to onboarding Input
	msgTx, err := boardingClient.lndClient.SendOutput(int64(btc_amnt), pkScript)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot send btc to output %v", err)
	}

	var txout *wire.TxOut
	var transferOutpoint *wire.OutPoint
	for index, output := range msgTx.TxOut {
		if bytes.Equal(output.PkScript, pkScript) {
			txout = output
			transferOutpoint = &wire.OutPoint{
				Hash:  msgTx.TxHash(),
				Index: uint32(index),
			}
			break
		}
	}

	if txout == nil || transferOutpoint == nil {
		return ArkBoardingTransfer{}, fmt.Errorf("Transfer Cannot be made")
	}

	btcTransferDetails := BtcTransferDetails{
		txout, transferOutpoint, btc_amnt, btcSpendingDetails,
	}

	// Ensure Btc and Asset Transfer Transfer
	waitForTransfers(bitcoinClient, serverTapClient, msgTx.TxHash(), addr_resp)

	// Derive Onboard Proof
	assetTransferProof, err := serverTapClient.ExportProof(assetId,
		assetTransferOutput.ScriptKey,
	)
	if err != nil {
		return ArkBoardingTransfer{}, fmt.Errorf("cannot export boarding proof %v", err)
	}

	assetTransferDetails := AssetTransferDetails{assetTransferOutput, assetSpendingDetails, asset_amnt, assetTransferProof}

	return ArkBoardingTransfer{assetTransferDetails, btcTransferDetails, boardingClient}, nil
}

func ConstructRoundIOFromBoarding(assetId []byte, boardingTransfer ArkBoardingTransfer, nextSpendingDetails ArkSpendingDetails, server *TapClient) (ChainTransfer, error) {
	//
	/// 1. Create Asset Transfer
	assetAmount := boardingTransfer.AssetTransferDetails.assetBoardingAmount
	previousAssetSpendingDetails := boardingTransfer.AssetTransferDetails.ArkSpendingDetails

	// Prepare an interactive send packet for the asset transfer
	fundedPkt := tappsbt.ForInteractiveSend(
		asset.ID(assetId),
		assetAmount,
		nextSpendingDetails.arkAssetScript.tapScriptKey,
		0, 0, 0,
		keychain.KeyDescriptor{
			PubKey: asset.NUMSPubKey,
		},
		asset.V0,
		&server.tapParams)

	// Encode Taproot Sibling
	scriptBranchPreimage := commitment.NewPreimageFromBranch(nextSpendingDetails.arkBtcScript.Branch)
	fundedPkt.Outputs[ROUND_ROOT_ASSET_OUTPUT_INDEX].AnchorOutputTapscriptSibling = &scriptBranchPreimage

	// Add asset input details
	createAndSetAssetInput(fundedPkt, ASSET_ANCHOR_ROUND_ROOT_INPUT_INDEX, boardingTransfer.AssetTransferDetails.AssetTransferOutput, assetId)

	// Add output details
	err := tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)
	if err != nil {
		return ChainTransfer{}, fmt.Errorf("cannot prepare Output %v", err)
	}

	// Insert asset witness details
	CreateAndInsertAssetWitness(previousAssetSpendingDetails, fundedPkt, boardingTransfer.user, server)
	vPackets := []*tappsbt.VPacket{fundedPkt}

	//
	//2. Create BTC Transfer
	btcAmount := boardingTransfer.btcTransferDetails.btcBoardingAmount + DUMMY_ASSET_BTC_AMOUNT - FEE
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		return ChainTransfer{}, fmt.Errorf("cannot prepare TransferBtc Packet %v", err)
	}

	// Update the BTC Output with the correct output
	transferBtcPkt.UnsignedTx.TxOut[ROUND_ROOT_ANCHOR_OUTPUT_INDEX].Value = int64(btcAmount)

	// Add BTC Boarding Input
	addBtcInputToPSBT(transferBtcPkt, boardingTransfer.btcTransferDetails)

	// Create Transfer Proofs
	server.CommitVirtualPsbts(
		transferBtcPkt, vPackets,
	)

	inputLength := 2
	spendingDetailsLists := make([]ArkSpendingDetails, inputLength)
	spendingDetailsLists[0] = boardingTransfer.AssetTransferDetails.ArkSpendingDetails
	spendingDetailsLists[1] = boardingTransfer.btcTransferDetails.arkSpendingDetails

	// Sign BTC inputs and generate witness lists
	btcAssetTxWitnessList, err := CreateBtcWitness(spendingDetailsLists, transferBtcPkt, inputLength, boardingTransfer.user, server)
	if err != nil {
		return ChainTransfer{}, fmt.Errorf("cannot Create BTC Witness %v", err)
	}

	for i := 0; i < inputLength; i++ {
		var buf bytes.Buffer
		err = psbt.WriteTxWitness(&buf, btcAssetTxWitnessList[i])
		if err != nil {
			return ChainTransfer{}, fmt.Errorf("failed to write BTC witness for input %v", err)
		}
		transferBtcPkt.Inputs[i].FinalScriptWitness = buf.Bytes()
	}

	// Finalise PSBT
	if err = psbt.MaybeFinalizeAll(transferBtcPkt); err != nil {
		return ChainTransfer{}, fmt.Errorf("failed to finaliste Psbt %v", err)
	}

	return DeriveUnpublishedChainTransfer(transferBtcPkt, vPackets[0].Outputs[0])
}
