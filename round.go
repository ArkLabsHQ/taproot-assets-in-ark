package taponark

import (
	"bytes"
	"context"
	"log"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/taprpc/tapdevrpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
)

func spendBoardingTransfer(assetId []byte, boardingTransfer ArkBoardingTransfer, transferAddress *address.Tap, server *TapClient) ChainTransfer {
	addresses := []*address.Tap{transferAddress}
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, TRANSFER_OUTPUT_INDEX)
	if err != nil {
		log.Fatalf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInput(fundedPkt, TRANSFER_INPUT_INDEX, boardingTransfer.previousOutput, assetId)

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

	unpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0])
	return unpublishedTransfer
}

func CreateRoundTransfer(boardingTransfer ArkBoardingTransfer, assetId []byte, user, server *TapClient, level uint64) ([]*proof.Proof, *proof.File) {
	unpublisedProofList := make([]*proof.Proof, level)

	userScriptKey, userInternalKey := user.GetNextKeys()
	serverScriptKey, serverInternalKey := server.GetNextKeys()

	arkScript, err := CreateRoundArkScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateRoundArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}

	addr_resp, err := server.GetBoardingAddress(arkScript.Branch, arkAssetScript.tapScriptKey, assetId, boardingTransfer.boardingAmount)
	if err != nil {
		log.Fatalf("cannot get address %v", err)
	}
	addr, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	unpublishedRootTransfer := spendBoardingTransfer(assetId, boardingTransfer, addr, server)
	btcControlBlock := extractControlBlock(arkScript, unpublishedRootTransfer.taprootAssetRoot)
	rootTransferDetails := ArkTransfer{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript}

	rootChainTransfer := ArkRoundChainTransfer{arkTransferDetails: rootTransferDetails, unpublishedTransfer: unpublishedRootTransfer}

	createIntermediateChainTransfer(assetId, boardingTransfer.boardingAmount, rootChainTransfer, level, user, server, &unpublisedProofList)

	// Broadcast the round Transfer
	bcoinClient := GetBitcoinClient()
	fullproof, err := server.client.ExportProof(context.TODO(), &taprpc.ExportProofRequest{
		AssetId:   assetId,
		ScriptKey: boardingTransfer.previousOutput.ScriptKey,
	})
	decodedFullProofFile, err := proof.DecodeFile(fullproof.RawProofFile)
	if err != nil {
		log.Fatalf("cannot fully decode proof file %v", err)
	}

	_, err = bcoinClient.SendRawTransaction(unpublishedRootTransfer.finalTx, true)
	if err != nil {
		log.Fatalf("cannot send raw transaction %v", err)
	}

	address1, err := bcoinClient.GetNewAddress("")
	if err != nil {
		log.Fatalf("cannot generate address %v", err)
	}
	maxretries := int64(3)
	blockhash, err := bcoinClient.GenerateToAddress(1, address1, &maxretries)
	if err != nil {
		log.Fatalf("cannot generate to address %v", err)
	}

	block, err := bcoinClient.GetBlock(blockhash[0])
	if err != nil {
		log.Fatalf("cannot get block %v", err)
	}

	blockheight, err := bcoinClient.GetBlockCount()
	if err != nil {
		log.Fatalf("cannot get block height %v", err)
	}

	proofParams := proof.BaseProofParams{
		Block:       block,
		Tx:          unpublishedRootTransfer.finalTx,
		BlockHeight: uint32(blockheight),
		TxIndex:     int(1),
	}

	transferProof := unpublishedRootTransfer.transferProof
	err = transferProof.UpdateTransitionProof(&proofParams)
	if err != nil {
		log.Fatalf("cannot update transfer proof %v", err)
	}

	err = decodedFullProofFile.AppendProof(*transferProof)
	if err != nil {
		log.Fatalf("cannot fully append proof file %v", err)
	}

}

func createIntermediateChainTransfer(assetId []byte, amount uint64, arkChainTransfer ArkRoundChainTransfer, level uint64, user, server *TapClient, unpublishedProofList *[]*proof.Proof) {
	if level == 0 {
		createFinalChainTransfer(assetId, amount, arkChainTransfer, level, user, server, unpublishedProofList)
		return
	}

	userScriptKey, userInternalKey := user.GetNextKeys()
	serverScriptKey, serverInternalKey := server.GetNextKeys()

	arkScript, err := CreateRoundArkScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateRoundArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}

	addr_resp, err := server.GetBoardingAddress(arkScript.Branch, arkAssetScript.tapScriptKey, assetId, amount)
	if err != nil {
		log.Fatalf("cannot get address %v", err)
	}
	transferAddress, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	addresses := []*address.Tap{transferAddress}
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, TRANSFER_OUTPUT_INDEX)
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

	unpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0])
	btcControlBlock := extractControlBlock(arkScript, unpublishedTransfer.taprootAssetRoot)

	*unpublishedProofList = append(*unpublishedProofList, unpublishedTransfer.transferProof)

	transferDetails := ArkTransfer{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript}
	interChainTransfer := ArkRoundChainTransfer{arkTransferDetails: transferDetails, unpublishedTransfer: unpublishedTransfer}

	createIntermediateChainTransfer(assetId, amount, interChainTransfer, level-1, user, server, unpublishedProofList)
}

func createFinalChainTransfer(assetId []byte, amount uint64, arkChainTransfer ArkRoundChainTransfer, level uint64, user, server *TapClient, unpublishedProofList *[]*proof.Proof) {
	addr_resp, err := user.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId: assetId,
		Amt:     amount,
	})
	if err != nil {
		log.Fatalf("cannot get address %v", err)
	}
	addr, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}
	addresses := make([]*address.Tap, 1)
	addresses[0] = addr
	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, TRANSFER_OUTPUT_INDEX)
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

	unpublishedTransfer := DeriveUnpublishedChainTransfer(signedPkt, finalizedTransferPackets[0])
	*unpublishedProofList = append(*unpublishedProofList, unpublishedTransfer.transferProof)

}

func publishTransfersAndSubmitProofs(assetId []byte, rootAddr *taprpc.Addr, unpublishedTransferList []*ChainTransfer, user *TapClient, server *TapClient) {
	bcoinClient := GetBitcoinClient()
	fullproof, err := server.client.ExportProof(context.TODO(), &taprpc.ExportProofRequest{
		AssetId:   assetId,
		ScriptKey: rootAddr.ScriptKey,
	})
	decodedFullProofFile, err := proof.DecodeFile(fullproof.RawProofFile)
	if err != nil {
		log.Fatalf("cannot fully decode proof file %v", err)
	}

	for _, unpublishedTransfer := range unpublishedTransferList {
		_, err = bcoinClient.SendRawTransaction(unpublishedTransfer.finalTx, true)
		if err != nil {
			log.Fatalf("cannot send raw transaction %v", err)
		}

		address1, err := bcoinClient.GetNewAddress("")
		if err != nil {
			log.Fatalf("cannot generate address %v", err)
		}
		maxretries := int64(3)
		blockhash, err := bcoinClient.GenerateToAddress(1, address1, &maxretries)
		if err != nil {
			log.Fatalf("cannot generate to address %v", err)
		}

		block, err := bcoinClient.GetBlock(blockhash[0])
		if err != nil {
			log.Fatalf("cannot get block %v", err)
		}

		blockheight, err := bcoinClient.GetBlockCount()
		if err != nil {
			log.Fatalf("cannot get block height %v", err)
		}

		proofParams := proof.BaseProofParams{
			Block:       block,
			Tx:          unpublishedTransfer.finalTx,
			BlockHeight: uint32(blockheight),
			TxIndex:     int(1),
		}

		transferProof := unpublishedTransfer.transferProof
		err = transferProof.UpdateTransitionProof(&proofParams)
		if err != nil {
			log.Fatalf("cannot update transfer proof %v", err)
		}

		err = decodedFullProofFile.AppendProof(*transferProof)
		if err != nil {
			log.Fatalf("cannot fully append proof file %v", err)
		}

	}

	encodedTransferProofFile, err := proof.EncodeFile(decodedFullProofFile)
	if err != nil {
		log.Fatalf("cannot encode proof File %v", err)
	}

	_, err = user.universeclient.ImportProof(context.TODO(), &tapdevrpc.ImportProofRequest{
		ProofFile:    encodedTransferProofFile,
		GenesisPoint: fullproof.GenesisPoint,
	})

	if err != nil {
		log.Fatalf("cannot import proof %v", err)
	}
}
