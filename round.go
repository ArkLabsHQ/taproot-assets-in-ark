package taponark

import (
	"bytes"
	"context"
	"log"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
)

func spendBoardingTransfer(assetId []byte, boardingTransfer ArkBoardingTransfer, transferAddress *address.Tap, server *TapClient) ChainTransfer {
	addresses := []*address.Tap{transferAddress}
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, BOARDING_TRANSFER_OUTPUT_INDEX)
	if err != nil {
		log.Fatalf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInput(fundedPkt, TRANSFER_INPUT_INDEX, boardingTransfer.assetPreviousOutput, assetId)

	// Note: This add output details
	tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	CreateAndInsertAssetWitness(boardingTransfer.arkTransferDetails, fundedPkt, boardingTransfer.user, server)

	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt, finalizedTransferPackets, _, _ := server.CommitVirtualPsbts(
		transferBtcPkt, vPackets, nil, -1,
	)

	btcTxWitness := CreateBtcWitness(boardingTransfer.arkTransferDetails, btcTransferPkt, boardingTransfer.user, server)
	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, btcTxWitness)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt.Inputs[TRANSFER_INPUT_INDEX].FinalScriptWitness = buf.Bytes()
	signedPkt := server.FinalizePacket(btcTransferPkt)

	unpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0].Outputs[BOARDING_TRANSFER_OUTPUT_INDEX])
	return unpublishedTransfer
}

func CreateRoundTransfer(boardingTransfer ArkBoardingTransfer, assetId []byte, user, server *TapClient, level uint64) ([]ProofTxMsg, []byte, string) {
	unpublisedProofList := make([]ProofTxMsg, 0)

	_, rootKeys := CreateAssetKeys(assetId, boardingTransfer.assetBoardingAmount, user, server)

	unpublishedRootTransfer := spendBoardingTransfer(assetId, boardingTransfer, rootKeys.address, server)
	btcControlBlock := extractControlBlock(rootKeys.arkScript, unpublishedRootTransfer.taprootAssetRoot)
	rootTransferDetails := ArkTransfer{btcControlBlock, rootKeys}
	rootChainTransfer := ArkRoundChainTransfer{arkTransferDetails: rootTransferDetails, unpublishedTransfer: unpublishedRootTransfer}

	createIntermediateChainTransfer(assetId, boardingTransfer.assetBoardingAmount, rootChainTransfer, level-1, user, server, &unpublisedProofList)

	// Broadcast the round Transfer
	fullproof, err := server.client.ExportProof(context.TODO(), &taprpc.ExportProofRequest{
		AssetId:   assetId,
		ScriptKey: boardingTransfer.assetPreviousOutput.ScriptKey,
	})
	if err != nil {
		log.Fatalf("cannot export proof %v", err)
	}

	bcoinClient := GetBitcoinClient()
	sendTxResult := bcoinClient.SendTransaction(unpublishedRootTransfer.finalTx)

	updatedProofFile := UpdateAndAppendProof(fullproof.RawProofFile, unpublishedRootTransfer.finalTx, unpublishedRootTransfer.transferProof, sendTxResult)

	log.Println("Round Proof Fetched and Updated")

	return unpublisedProofList, updatedProofFile, fullproof.GenesisPoint

}

func createIntermediateChainTransfer(assetId []byte, amount uint64, arkChainTransfer ArkRoundChainTransfer, level uint64, user, server *TapClient, unpublishedProofList *[]ProofTxMsg) {
	if level == 0 {
		createFinalChainTransfer(assetId, amount, arkChainTransfer, user, server, unpublishedProofList)
		return
	}

	_, leftKeys := CreateAssetKeys(assetId, amount/2, user, server)
	_, rightKeys := CreateAssetKeys(assetId, amount/2, user, server)

	addresses := []*address.Tap{leftKeys.address, rightKeys.address}
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, LEFT_TRANSFER_OUTPUT_INDEX)
	if err != nil {
		log.Fatalf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInputIntermediate(fundedPkt, TRANSFER_INPUT_INDEX, arkChainTransfer.unpublishedTransfer, assetId)

	// Note: This add output details
	tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	CreateAndInsertAssetWitness(arkChainTransfer.arkTransferDetails, fundedPkt, user, server)

	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		log.Fatal(err)
	}

	//Adds Fees and commit
	btcTransferPkt, finalizedTransferPackets, _, _ := server.CommitVirtualPsbts(
		transferBtcPkt, vPackets, nil, -1,
	)

	btcTxWitness := CreateBtcWitness(arkChainTransfer.arkTransferDetails, btcTransferPkt, user, server)
	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, btcTxWitness)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt.Inputs[TRANSFER_INPUT_INDEX].FinalScriptWitness = buf.Bytes()
	signedPkt := server.FinalizePacket(btcTransferPkt)

	// derive Left and Right Unpublished Transfers
	leftUnpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0].Outputs[LEFT_TRANSFER_OUTPUT_INDEX])
	rightUnpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0].Outputs[RIGHT_TRANSFER_OUTPUT_INDEX])

	// derive Left and Right Control Blocks
	leftBtcControlBlock := extractControlBlock(leftKeys.arkScript, leftUnpublishedTransfer.taprootAssetRoot)
	rightBtcControlBlock := extractControlBlock(rightKeys.arkScript, rightUnpublishedTransfer.taprootAssetRoot)

	// derive and  Left and Right Proofs Details to the ProofList
	leftProofTxMsg := ProofTxMsg{TxMsg: leftUnpublishedTransfer.finalTx, Proof: leftUnpublishedTransfer.transferProof}
	rightProofTxMsg := ProofTxMsg{TxMsg: rightUnpublishedTransfer.finalTx, Proof: rightUnpublishedTransfer.transferProof}
	*unpublishedProofList = append(*unpublishedProofList, leftProofTxMsg, rightProofTxMsg)

	leftTransferDetails := ArkTransfer{leftBtcControlBlock, leftKeys}
	leftIntermediateChainTransfer := ArkRoundChainTransfer{arkTransferDetails: leftTransferDetails, unpublishedTransfer: leftUnpublishedTransfer}

	rightTransferDetails := ArkTransfer{rightBtcControlBlock, rightKeys}
	rightIntermediateChainTransfer := ArkRoundChainTransfer{arkTransferDetails: rightTransferDetails, unpublishedTransfer: rightUnpublishedTransfer}

	log.Println("Intermediate Asset Transferred")

	// Recursively create the next level of transfers
	createIntermediateChainTransfer(assetId, amount/2, leftIntermediateChainTransfer, level-1, user, server, unpublishedProofList)
	createIntermediateChainTransfer(assetId, amount/2, rightIntermediateChainTransfer, level-1, user, server, unpublishedProofList)

}

func createFinalChainTransfer(assetId []byte, amount uint64, arkChainTransfer ArkRoundChainTransfer, user, server *TapClient, unpublishedProofList *[]ProofTxMsg) {
	left_addr_resp, err := user.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId: assetId,
		Amt:     amount / 2,
	})

	if err != nil {
		log.Fatalf("cannot get left address %v", err)
	}

	right_addr_resp, err := user.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId: assetId,
		Amt:     amount / 2,
	})

	if err != nil {
		log.Fatalf("cannot get right address %v", err)
	}

	left_addr, err := address.DecodeAddress(left_addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	right_addr, err := address.DecodeAddress(right_addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	addresses := []*address.Tap{left_addr, right_addr}
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, LEFT_TRANSFER_OUTPUT_INDEX)
	if err != nil {
		log.Fatalf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInputIntermediate(fundedPkt, TRANSFER_INPUT_INDEX, arkChainTransfer.unpublishedTransfer, assetId)

	// Note: This add output details
	tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	CreateAndInsertAssetWitness(arkChainTransfer.arkTransferDetails, fundedPkt, user, server)

	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt, finalizedTransferPackets, _, _ := server.CommitVirtualPsbts(
		transferBtcPkt, vPackets, nil, -1,
	)

	btcTxWitness := CreateBtcWitness(arkChainTransfer.arkTransferDetails, btcTransferPkt, user, server)
	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, btcTxWitness)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt.Inputs[TRANSFER_INPUT_INDEX].FinalScriptWitness = buf.Bytes()
	signedPkt := server.FinalizePacket(btcTransferPkt)

	leftUnpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0].Outputs[LEFT_TRANSFER_OUTPUT_INDEX])
	rightUnpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0].Outputs[RIGHT_TRANSFER_OUTPUT_INDEX])

	leftProofTxMsg := ProofTxMsg{TxMsg: leftUnpublishedTransfer.finalTx, Proof: leftUnpublishedTransfer.transferProof}
	rightProofTxMsg := ProofTxMsg{TxMsg: rightUnpublishedTransfer.finalTx, Proof: rightUnpublishedTransfer.transferProof}

	*unpublishedProofList = append(*unpublishedProofList, leftProofTxMsg, rightProofTxMsg)

	log.Println("Final Asset Transferred")

}
