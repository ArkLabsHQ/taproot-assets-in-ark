package taponark

import (
	"bytes"
	"context"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
	"github.com/lightningnetwork/lnd/keychain"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
)

func CreateRoundTransfer(boardingTransfer ArkBoardingTransfer, assetId []byte, user, server *TapClient, level uint64, bitcoinClient BitcoinClient) ([]VirtualTxOut, []byte, error) {
	vtxoList := make([]VirtualTxOut, 0)

	roundRootSpendingDetails, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create Round Spending Details %v", err)
	}

	roundRootTransfer, err := ConstructRoundIOFromBoarding(assetId, boardingTransfer, roundRootSpendingDetails, server)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot construct Round IO From Boarding %v", err)
	}

	// Insert Control Block
	btcControlBlock := extractControlBlock(roundRootSpendingDetails.arkBtcScript, roundRootTransfer.taprootAssetRoot)
	roundRootSpendingDetails.arkBtcScript.controlBlock = btcControlBlock

	// Create Level 1 Branch Transaction
	createIntermediateChainTransfer(assetId, roundRootSpendingDetails, roundRootTransfer, level-1, user, server, &vtxoList)

	sendTxResult, err := bitcoinClient.SendTransaction(roundRootTransfer.finalTx)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot send transaction %v", err)
	}

	rootProofFile, err := UpdateAndAppendProof(boardingTransfer.AssetTransferDetails.RawProofFile, roundRootTransfer.finalTx, roundRootTransfer.transferProof, sendTxResult)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot update and append proof %v", err)
	}

	log.Printf("\nRound Transaction Hash %s", roundRootTransfer.finalTx.TxHash().String())

	return vtxoList, rootProofFile, nil

}

func createIntermediateChainTransfer(assetId []byte, inputSpendingDetails ArkSpendingDetails, inputChainTransfer ChainTransfer, level uint64, user, server *TapClient, vtxoList *[]VirtualTxOut) error {
	if level == 0 {
		return createFinalChainTransfer(assetId, inputSpendingDetails, inputChainTransfer, user, server, vtxoList)
	}

	leftOutputSpendingDetails, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return fmt.Errorf("cannot create Round Spending Details %v", err)
	}

	rightOutputSpendingDetail, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return fmt.Errorf("cannot create Round Spending Details %v", err)
	}

	branchBtcAmount := (inputChainTransfer.anchorValue - int64(FEE)) / 2 // Fee
	branchAssetAmount := inputChainTransfer.assetAmount / 2

	fundedPkt := tappsbt.ForInteractiveSend(asset.ID(assetId), branchAssetAmount, leftOutputSpendingDetails.arkAssetScript.tapScriptKey, 0, 0, 0,
		keychain.KeyDescriptor{
			PubKey: asset.NUMSPubKey,
		}, asset.V0, &server.tapParams)

	fundedPkt.Outputs[0].Type = tappsbt.TypeSplitRoot
	leftBranchScriptBranchPreimage := commitment.NewPreimageFromBranch(leftOutputSpendingDetails.arkBtcScript.Branch)
	fundedPkt.Outputs[0].AnchorOutputTapscriptSibling = &leftBranchScriptBranchPreimage

	tappsbt.AddOutput(fundedPkt, branchAssetAmount, rightOutputSpendingDetail.arkAssetScript.tapScriptKey, 1,
		keychain.KeyDescriptor{
			PubKey: asset.NUMSPubKey,
		}, asset.V0)
	rightBranchScriptBranchPreimage := commitment.NewPreimageFromBranch(rightOutputSpendingDetail.arkBtcScript.Branch)
	fundedPkt.Outputs[1].AnchorOutputTapscriptSibling = &rightBranchScriptBranchPreimage

	// Note: This add input details
	createAndSetInputIntermediate(fundedPkt, inputChainTransfer, assetId)
	// Note: This add output details
	err = tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)
	if err != nil {
		return fmt.Errorf("cannot prepare Output %v", err)
	}

	CreateAndInsertAssetWitness(inputSpendingDetails, fundedPkt, user, server)

	// Add Btc Output Amount
	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		return fmt.Errorf("cannot prepare TransferBtc Packet %v", err)
	}
	transferBtcPkt.UnsignedTx.TxOut[0].Value = int64(branchBtcAmount)
	transferBtcPkt.UnsignedTx.TxOut[1].Value = int64(branchBtcAmount)

	//Adds Fees and commit
	server.CommitVirtualPsbts(
		transferBtcPkt, vPackets,
	)

	inputLength := 1
	spendingDetailsLists := make([]ArkSpendingDetails, inputLength)
	spendingDetailsLists[0] = inputSpendingDetails

	// Sign BTC Input
	btcTxWitness, err := CreateBtcWitness(spendingDetailsLists, transferBtcPkt, inputLength, user, server)
	if err != nil {
		return fmt.Errorf("cannot Create BTC Witness %v", err)
	}

	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, btcTxWitness[0])
	if err != nil {
		return fmt.Errorf("cannot write Tx Witness %v", err)
	}

	transferBtcPkt.Inputs[0].FinalScriptWitness = buf.Bytes()
	// Finalise PSST
	err = psbt.MaybeFinalizeAll(transferBtcPkt)

	if err != nil {
		return fmt.Errorf("failed to finaliste Psbt %v", err)
	}

	// derive Left and Right Unpublished Transfers
	leftUnpublishedTransfer, err := DeriveUnpublishedChainTransfer(transferBtcPkt, vPackets[0].Outputs[0])
	if err != nil {
		return fmt.Errorf("cannot derive Unpublished Chain Transfer %v", err)
	}

	rightUnpublishedTransfer, err := DeriveUnpublishedChainTransfer(transferBtcPkt, vPackets[0].Outputs[1])
	if err != nil {
		return fmt.Errorf("cannot derive Unpublished Chain Transfer %v", err)
	}

	// derive Left and Right Control Blocks
	leftBtcControlBlock := extractControlBlock(leftOutputSpendingDetails.arkBtcScript, leftUnpublishedTransfer.taprootAssetRoot)
	rightBtcControlBlock := extractControlBlock(rightOutputSpendingDetail.arkBtcScript, rightUnpublishedTransfer.taprootAssetRoot)

	// derive and  Left and Right Proofs Details to the ProofList
	leftOutputProofTxMsg := VirtualTxOut{TxMsg: leftUnpublishedTransfer.finalTx, AssetProof: leftUnpublishedTransfer.transferProof, Index: 0, vtxoType: BRANCH, BtcAmount: branchBtcAmount, AssetAmount: branchAssetAmount}
	rightOutputProofTxMsg := VirtualTxOut{TxMsg: rightUnpublishedTransfer.finalTx, AssetProof: rightUnpublishedTransfer.transferProof, Index: 1, vtxoType: BRANCH, BtcAmount: branchBtcAmount, AssetAmount: branchAssetAmount}
	*vtxoList = append(*vtxoList, leftOutputProofTxMsg, rightOutputProofTxMsg)

	leftOutputSpendingDetails.arkBtcScript.controlBlock = leftBtcControlBlock
	rightOutputSpendingDetail.arkBtcScript.controlBlock = rightBtcControlBlock

	// Recursively create the next level of transfers
	err = createIntermediateChainTransfer(assetId, leftOutputSpendingDetails, leftUnpublishedTransfer, level-1, user, server, vtxoList)
	if err != nil {
		return fmt.Errorf("cannot create Intermediate Chain Transfer %v", err)
	}

	err = createIntermediateChainTransfer(assetId, rightOutputSpendingDetail, rightUnpublishedTransfer, level-1, user, server, vtxoList)
	if err != nil {
		return fmt.Errorf("cannot create Intermediate Chain Transfer %v", err)
	}

	return nil
}

func createFinalChainTransfer(assetId []byte, inputSpendingDetails ArkSpendingDetails, inputChainTransfer ChainTransfer, user, server *TapClient, vtxoList *[]VirtualTxOut) error {
	assetOutputIndex := 1
	assetAmountInBtc := DUMMY_ASSET_BTC_AMOUNT
	changeAssetInBtc := DUMMY_ASSET_BTC_AMOUNT
	btcAmount := inputChainTransfer.anchorValue - int64(assetAmountInBtc) - int64(changeAssetInBtc) - int64(FEE)
	// TODO (Joshua Kindly Improve to only have two output)
	asset_addr_resp, err := user.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId: assetId,
		Amt:     inputChainTransfer.assetAmount,
	})

	if err != nil {
		return fmt.Errorf("cannot get asset left address %v", err)
	}

	asset_addr, err := address.DecodeAddress(asset_addr_resp.Encoded, &server.tapParams)
	if err != nil {
		return fmt.Errorf("cannot decode address %v", err)
	}

	btc_addr_resp, err := user.lndClient.wallet.NextAddr(context.TODO(), &walletrpc.AddrRequest{
		Type:   walletrpc.AddressType_TAPROOT_PUBKEY,
		Change: false,
	})

	if err != nil {
		return fmt.Errorf("cannot get btc right address %v", err)
	}

	btc_addr, err := btcutil.DecodeAddress(btc_addr_resp.Addr, &server.chainParams)
	if err != nil {
		return fmt.Errorf("cannot decode address %v", err)
	}

	// create public key
	parsedInternalKey, err := schnorr.ParsePubKey(btc_addr.ScriptAddress())
	if err != nil {
		return fmt.Errorf("cannot parse Internal Key %v", err)
	}

	// Import watch only wallet
	_, err = user.lndClient.wallet.ImportPublicKey(context.TODO(), &walletrpc.ImportPublicKeyRequest{
		PublicKey:   schnorr.SerializePubKey(parsedInternalKey),
		AddressType: walletrpc.AddressType_TAPROOT_PUBKEY,
	})

	if err != nil {
		return fmt.Errorf("cannot import watch only %v", err)
	}

	addresses := []*address.Tap{asset_addr}
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, uint32(assetOutputIndex))
	if err != nil {
		return fmt.Errorf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInputIntermediate(fundedPkt, inputChainTransfer, assetId)

	// Note: This add output details
	tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	CreateAndInsertAssetWitness(inputSpendingDetails, fundedPkt, user, server)

	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		return fmt.Errorf("cannot prepare TransferBtc Packet %v", err)
	}
	addBtcOutput(transferBtcPkt, uint64(btcAmount), parsedInternalKey)

	server.CommitVirtualPsbts(
		transferBtcPkt, vPackets,
	)

	inputLength := 1
	spendingDetailsLists := make([]ArkSpendingDetails, inputLength)
	spendingDetailsLists[0] = inputSpendingDetails

	btcTxWitness, err := CreateBtcWitness(spendingDetailsLists, transferBtcPkt, inputLength, user, server)
	if err != nil {
		return fmt.Errorf("cannot Create BTC Witness %v", err)
	}

	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, btcTxWitness[0])
	if err != nil {
		return fmt.Errorf("failed to write BTC witness for input %v", err)
	}

	transferBtcPkt.Inputs[0].FinalScriptWitness = buf.Bytes()

	// Finalise PSBT
	err = psbt.MaybeFinalizeAll(transferBtcPkt)
	if err != nil {
		return fmt.Errorf("failed to finaliste Psbt %v", err)
	}

	// derive Asset Unpublished Transfers
	unpublishedTransfer, err := DeriveUnpublishedChainTransfer(transferBtcPkt, vPackets[0].Outputs[assetOutputIndex])
	if err != nil {
		return fmt.Errorf("cannot derive Unpublished Chain Transfer %v", err)
	}
	assetVtxo := VirtualTxOut{TxMsg: unpublishedTransfer.finalTx, AssetProof: unpublishedTransfer.transferProof, Index: assetOutputIndex, vtxoType: ASSET, AssetAmount: inputChainTransfer.assetAmount}

	// derive Btc Unpublished Transfers
	btcOutputIndex := 2
	btcVtxo := VirtualTxOut{TxMsg: unpublishedTransfer.finalTx, AssetProof: nil, Index: btcOutputIndex, vtxoType: BTC, BtcAmount: btcAmount}

	*vtxoList = append(*vtxoList, assetVtxo, btcVtxo)

	return nil
}
