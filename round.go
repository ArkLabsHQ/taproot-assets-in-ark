package taponark

import (
	"bytes"
	"context"
	"log"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
	"github.com/lightningnetwork/lnd/keychain"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
)

func CreateRoundTransfer(inputAssetProofFile []byte, inputSpendingDetails ArkSpendingDetails, inputTransfer ChainTransfer, assetId []byte, user, server *TapClient, level uint64) ([]ProofTxMsg, []byte) {
	unpublisedProofList := make([]ProofTxMsg, 0)

	btcControlBlock := extractControlBlock(inputSpendingDetails.arkBtcScript, inputTransfer.taprootAssetRoot)
	inputSpendingDetails.arkBtcScript.controlBlock = btcControlBlock

	createIntermediateChainTransfer(assetId, inputSpendingDetails, inputTransfer, level-1, user, server, &unpublisedProofList)

	bcoinClient := GetBitcoinClient()
	sendTxResult := bcoinClient.SendTransaction(inputTransfer.finalTx)

	rootProofFile := UpdateAndAppendProof(inputAssetProofFile, inputTransfer.finalTx, inputTransfer.transferProof, sendTxResult)

	log.Println("Round Proof Fetched and Updated")

	return unpublisedProofList, rootProofFile

}

func createIntermediateChainTransfer(assetId []byte, inputSpendingDetails ArkSpendingDetails, inputChainTransfer ChainTransfer, level uint64, user, server *TapClient, unpublishedProofList *[]ProofTxMsg) {
	if level == 0 {
		createFinalChainTransfer(assetId, inputSpendingDetails, inputChainTransfer, user, server, unpublishedProofList)
		return
	}

	leftOutputSpendingDetails := CreateRoundSpendingDetails(user, server)
	rightOutputSpendingDetail := CreateRoundSpendingDetails(user, server)

	fee := int64(10_000)
	btcAmount := (inputChainTransfer.anchorValue - fee) / 2 // Fee
	assetAmount := inputChainTransfer.assetAmount / 2

	fundedPkt := tappsbt.ForInteractiveSend(asset.ID(assetId), assetAmount, leftOutputSpendingDetails.arkAssetScript.tapScriptKey, 0, 0, 0,
		keychain.KeyDescriptor{
			PubKey: asset.NUMSPubKey,
		}, asset.V0, &address.RegressionNetTap)

	fundedPkt.Outputs[0].Type = tappsbt.TypeSplitRoot

	tappsbt.AddOutput(fundedPkt, assetAmount, rightOutputSpendingDetail.arkAssetScript.tapScriptKey, 1,
		keychain.KeyDescriptor{
			PubKey: asset.NUMSPubKey,
		}, asset.V0)

	// Note: This add input details
	createAndSetInputIntermediate(fundedPkt, TRANSFER_INPUT_INDEX, inputChainTransfer, assetId)
	// Note: This add output details
	err := tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	if err != nil {
		log.Fatalf("cannot prepare Output %v", err)
	}

	CreateAndInsertAssetWitness(inputSpendingDetails, fundedPkt, user, server)

	// Add Btc Output Amount
	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		log.Fatal(err)
	}
	transferBtcPkt.UnsignedTx.TxOut[0].Value = int64(btcAmount)
	transferBtcPkt.UnsignedTx.TxOut[1].Value = int64(btcAmount)

	//Adds Fees and commit
	server.CommitVirtualPsbts(
		transferBtcPkt, vPackets,
	)

	inputLength := 1
	spendingDetailsLists := make([]ArkSpendingDetails, inputLength)
	spendingDetailsLists[0] = inputSpendingDetails

	// Sign BTC Input
	btcTxWitness := CreateBtcWitness(spendingDetailsLists, transferBtcPkt, inputLength, user, server)

	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, btcTxWitness[0])
	if err != nil {
		log.Fatal(err)
	}

	transferBtcPkt.Inputs[TRANSFER_INPUT_INDEX].FinalScriptWitness = buf.Bytes()
	// Finalise PSBT
	err = psbt.MaybeFinalizeAll(transferBtcPkt)

	if err != nil {
		log.Fatalf("failed to finaliste Psbt %v", err)
	}

	// derive Left and Right Unpublished Transfers
	leftUnpublishedTransfer := DeriveUnpublishedChainTransfer(transferBtcPkt, vPackets[0].Outputs[0])
	rightUnpublishedTransfer := DeriveUnpublishedChainTransfer(transferBtcPkt, vPackets[0].Outputs[1])

	// derive Left and Right Control Blocks
	leftBtcControlBlock := extractControlBlock(leftOutputSpendingDetails.arkBtcScript, leftUnpublishedTransfer.taprootAssetRoot)
	rightBtcControlBlock := extractControlBlock(rightOutputSpendingDetail.arkBtcScript, rightUnpublishedTransfer.taprootAssetRoot)

	// derive and  Left and Right Proofs Details to the ProofList
	leftOutputProofTxMsg := ProofTxMsg{TxMsg: leftUnpublishedTransfer.finalTx, Proof: leftUnpublishedTransfer.transferProof}
	rightOutputProofTxMsg := ProofTxMsg{TxMsg: rightUnpublishedTransfer.finalTx, Proof: rightUnpublishedTransfer.transferProof}
	*unpublishedProofList = append(*unpublishedProofList, leftOutputProofTxMsg, rightOutputProofTxMsg)

	leftOutputSpendingDetails.arkBtcScript.controlBlock = leftBtcControlBlock
	rightOutputSpendingDetail.arkBtcScript.controlBlock = rightBtcControlBlock

	log.Println("Intermediate Asset Transferred")
	// Recursively create the next level of transfers
	createIntermediateChainTransfer(assetId, leftOutputSpendingDetails, leftUnpublishedTransfer, level-1, user, server, unpublishedProofList)
	createIntermediateChainTransfer(assetId, rightOutputSpendingDetail, rightUnpublishedTransfer, level-1, user, server, unpublishedProofList)

}

func createFinalChainTransfer(assetId []byte, inputSpendingDetails ArkSpendingDetails, inputChainTransfer ChainTransfer, user, server *TapClient, unpublishedProofList *[]ProofTxMsg) {
	assetAmountInBtc := 1_000
	changeAssetInBtc := 1_000
	fee := 10_000
	btcAmount := inputChainTransfer.anchorValue - int64(assetAmountInBtc) - int64(changeAssetInBtc) - int64(fee)

	asset_addr_resp, err := user.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId: assetId,
		Amt:     inputChainTransfer.assetAmount,
	})

	if err != nil {
		log.Fatalf("cannot get asset left address %v", err)
	}

	asset_addr, err := address.DecodeAddress(asset_addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	btc_addr_resp, err := user.lndClient.wallet.NextAddr(context.TODO(), &walletrpc.AddrRequest{
		Type:   walletrpc.AddressType_TAPROOT_PUBKEY,
		Change: false,
	})

	if err != nil {
		log.Fatalf("cannot get btc right address %v", err)
	}

	btc_addr, err := btcutil.DecodeAddress(btc_addr_resp.Addr, &chaincfg.RegressionNetParams)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	addresses := []*address.Tap{asset_addr}
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, LEFT_TRANSFER_OUTPUT_INDEX)
	if err != nil {
		log.Fatalf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInputIntermediate(fundedPkt, TRANSFER_INPUT_INDEX, inputChainTransfer, assetId)

	// Note: This add output details
	tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	CreateAndInsertAssetWitness(inputSpendingDetails, fundedPkt, user, server)

	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		log.Fatal(err)
	}
	addBtcOutput(transferBtcPkt, uint64(btcAmount), btc_addr)

	server.CommitVirtualPsbts(
		transferBtcPkt, vPackets,
	)

	inputLength := 1
	spendingDetailsLists := make([]ArkSpendingDetails, inputLength)
	spendingDetailsLists[0] = inputSpendingDetails

	btcTxWitness := CreateBtcWitness(spendingDetailsLists, transferBtcPkt, inputLength, user, server)
	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, btcTxWitness[0])
	if err != nil {
		log.Fatal(err)
	}

	transferBtcPkt.Inputs[TRANSFER_INPUT_INDEX].FinalScriptWitness = buf.Bytes()

	// Finalise PSBT
	err = psbt.MaybeFinalizeAll(transferBtcPkt)
	if err != nil {
		log.Fatalf("failed to finaliste Psbt %v", err)
	}

	// derive Asset Unpublished Transfers
	unpublishedTransfer := DeriveUnpublishedChainTransfer(transferBtcPkt, vPackets[0].Outputs[LEFT_TRANSFER_OUTPUT_INDEX])
	assetProofTxMsg := ProofTxMsg{TxMsg: unpublishedTransfer.finalTx, Proof: unpublishedTransfer.transferProof}

	*unpublishedProofList = append(*unpublishedProofList, assetProofTxMsg)

	log.Println("Final Asset Transferred")

}
