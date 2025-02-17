package taponark

import (
	"log"

	"github.com/btcsuite/btcd/txscript"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
)

// user tap client

func SpendToBoardingTransaction(assetId []byte, amnt uint64, lockHeight uint32, userTapClient, serverTapClient *TapClient) ArkTransferOutputDetails {
	userScriptKey, userInternalKey := userTapClient.GetNextKeys()
	serverScriptKey, serverInternalKey := serverTapClient.GetNextKeys()

	// 1. Create Both Bording Ark Script and Boarding Ark Asset Script
	arkScript, err := CreateBoardingArkScript(userInternalKey.PubKey, serverInternalKey.PubKey, lockHeight)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateBoardingArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey, lockHeight)
	if err != nil {
		log.Fatal(err)
	}

	addr, err := serverTapClient.GetBoardingAddress(arkScript.Branch, arkAssetScript.tapScriptKey, assetId, amnt)
	if err != nil {
		log.Fatal(err)
	}

	// 2. Send From User To Boarding Address
	sendResp, err := userTapClient.SendAsset(addr)

	// spend from Boarding Address
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}
	// Transfer is always output 1
	transferOutput := sendResp.Transfer.Outputs[1]
	multiSigOutAnchor := transferOutput.Anchor
	rightNodeHash := arkScript.Right.TapHash()
	inclusionproof := append(rightNodeHash[:], multiSigOutAnchor.TaprootAssetRoot[:]...)
	btcControlBlock.InclusionProof = inclusionproof
	rootHash := btcControlBlock.RootHash(arkScript.Left.Script)
	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		btcControlBlock.OutputKeyYIsOdd = true
	}

	//TODO (Joshua) Ensure To Improve
	log.Println("Asset Transfered Please Mine")
	serverTapClient.IncomingTransferEvent(addr)

	return ArkTransferOutputDetails{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript, transferOutput}
}

// func SpendFromBoardingTransaction(spendAddr *taprpc.Addr, userTapClient, serverTapClient *TapClient, outDetails ArkBoardingTransferOutputDetails) ProofOutputDetail {
// 	spendRecipients := map[string]uint64{
// 		spendAddr.Encoded: spendAddr.Amount,
// 	}
// 	// Note : This is the point in which Input and Output are Created
// 	spendFundResp, err := serverTapClient.wallet.FundVirtualPsbt(
// 		context.TODO(), &assetwalletrpc.FundVirtualPsbtRequest{
// 			Template: &assetwalletrpc.FundVirtualPsbtRequest_Raw{
// 				Raw: &assetwalletrpc.TxTemplate{
// 					Recipients: spendRecipients,
// 				},
// 			},
// 		},
// 	)

// 	log.Println("created spend fund Virtual PSBT")
// 	fundedSpendPkt := deserializeVPacket(spendFundResp.FundedPsbt)
// 	_, userSessionId := userTapClient.partialSignAssetTransfer(fundedSpendPkt,
// 		&outDetails.arkAssetScript.leaves[0], outDetails.userScriptKey.RawKey, outDetails.arkAssetScript.userNonce, outDetails.serverScriptKey.RawKey.PubKey, outDetails.arkAssetScript.serverNonce.PubNonce)

// 	log.Println("created asset partial sig for user")

// 	serverPartialSig, _ := serverTapClient.partialSignAssetTransfer(fundedSpendPkt,
// 		&outDetails.arkAssetScript.leaves[0], outDetails.serverScriptKey.RawKey, outDetails.arkAssetScript.serverNonce, outDetails.userScriptKey.RawKey.PubKey, outDetails.arkAssetScript.userNonce.PubNonce)

// 	log.Println("created asset partial for server")

// 	transferAssetWitness := userTapClient.combineSigs(userSessionId, serverPartialSig, outDetails.arkAssetScript.leaves[0], outDetails.arkAssetScript.tree, outDetails.arkAssetScript.controlBlock)

// 	// update transferAsset Witnesss
// 	for idx := range fundedSpendPkt.Outputs {
// 		asset := fundedSpendPkt.Outputs[idx].Asset
// 		firstPrevWitness := &asset.PrevWitnesses[0]
// 		// Verify that this is a split asset
// 		if asset.HasSplitCommitmentWitness() {
// 			rootAsset := firstPrevWitness.SplitCommitment.RootAsset
// 			firstPrevWitness = &rootAsset.PrevWitnesses[0]
// 		}
// 		firstPrevWitness.TxWitness = transferAssetWitness
// 	}

// 	vPackets := []*tappsbt.VPacket{fundedSpendPkt}
// 	// Commit Inputs and Outputs
// 	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	// Note This is where the Proof is Inserted
// 	btcTransferPkt, finalizedTransferPackets, _, commitResp := serverTapClient.CommitVirtualPsbts(
// 		transferBtcPkt, vPackets, nil, -1,
// 	)

// 	// sign Btc Transaction
// 	btcControlBlockBytes, err := outDetails.btcControlBlock.ToBytes()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	assetInputIdx := uint32(0)
// 	serverBtcPartialSig := serverTapClient.partialSignBtcTransfer(
// 		btcTransferPkt, assetInputIdx,
// 		outDetails.serverInternalKey, btcControlBlockBytes, outDetails.arkScript.Left,
// 	)
// 	userBtcPartialSig := userTapClient.partialSignBtcTransfer(
// 		btcTransferPkt, assetInputIdx,
// 		outDetails.userInternalKey, btcControlBlockBytes, outDetails.arkScript.Left,
// 	)

// 	txWitness := wire.TxWitness{
// 		serverBtcPartialSig,
// 		userBtcPartialSig,
// 		outDetails.arkScript.Left.Script,
// 		btcControlBlockBytes,
// 	}
// 	var buf bytes.Buffer
// 	err = psbt.WriteTxWitness(&buf, txWitness)
// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	btcTransferPkt.Inputs[assetInputIdx].FinalScriptWitness = buf.Bytes()
// 	signedPkt := serverTapClient.FinalizePacket(btcTransferPkt)

// 	logAndPublishResponse := serverTapClient.LogAndPublish(signedPkt, finalizedTransferPackets, nil,
// 		commitResp,
// 	)

// 	if err != nil {
// 		log.Fatalf("cant decode virtual Psbt %w", err)
// 	}

// 	return ProofOutputDetail{assetTransfer: logAndPublishResponse.Transfer, transferPacket: finalizedTransferPackets[0]}

// 	//TODO (Joshua) Ensure To Improve

// 	// log.Println("Asset Transfered Please Mine")

// 	// // wait for transfer to reach
// 	// serverTapClient.IncomingTransferEvent(addr)

// }

// func RunBoardingAndSpend() {
// 	currentBlockHeight := 300
// 	lockBlockHeight := currentBlockHeight + 4320

// 	assetId, _ := hex.DecodeString("7d8a7fe5d768f53d286c399350412e857e0014481a8fb8faf10bc62b31e736fc")
// 	ammt := 10

// 	userLndClient := InitLndClient(userLndRpcHostPort, userLndRpcPort, userLndTLSCert, userLndMacaroon)
// 	userTapClient := InitTapClient(userRPCHostPort, userRPCPort, userTapTLSCertFilename, userTapMacaroon, userLndClient)

// 	serverLndClient := InitLndClient(serverLndRPCHostPort, serverLndRpcPort, serverLndTLSCert, serverLndMacaroon)
// 	serverTapClient := InitTapClient(serverRPCHostPort, serverRPCPort, serverTapTLSCertFilename, serverTapMacaroon, serverLndClient)

// 	userScriptKey, userInternalKey := userTapClient.GetNextKeys()
// 	serverScriptKey, serverInternalKey := serverTapClient.GetNextKeys()

// 	// 1. Create Both Bording Ark Script and Boarding Ark Asset Script
// 	arkScript, err := CreateBoardingArkScript(userInternalKey.PubKey, serverInternalKey.PubKey, uint32(lockBlockHeight))
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	arkAssetScript := CreateBoardingArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey, uint32(lockBlockHeight))

// 	addr, err := serverTapClient.GetBoardingAddress(arkScript.Branch, arkAssetScript.tapScriptKey, assetId, uint64(ammt))

// 	// 2. Send From User To Boarding Address
// 	sendResp, err := userTapClient.client.SendAsset(context.TODO(), &taprpc.SendAssetRequest{
// 		TapAddrs: []string{addr.Encoded},
// 	})

// 	// spend from Boarding Address
// 	btcInternalKey := asset.NUMSPubKey
// 	btcControlBlock := &txscript.ControlBlock{
// 		LeafVersion: txscript.BaseLeafVersion,
// 		InternalKey: btcInternalKey,
// 	}
// 	multiSigOutAnchor := sendResp.Transfer.Outputs[1].Anchor
// 	rightNodeHash := arkScript.Right.TapHash()
// 	inclusionproof := append(rightNodeHash[:], multiSigOutAnchor.TaprootAssetRoot[:]...)
// 	btcControlBlock.InclusionProof = inclusionproof
// 	rootHash := btcControlBlock.RootHash(arkScript.Left.Script)
// 	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
// 	if tapKey.SerializeCompressed()[0] ==
// 		secp256k1.PubKeyFormatCompressedOdd {

// 		btcControlBlock.OutputKeyYIsOdd = true
// 	}

// 	if err != nil {
// 		log.Fatal("error", err)
// 	}

// 	log.Println("Asset Transfered Please Mine")

// 	// wait for transfer to reach
// 	serverTapClient.IncomingTransferEvent(addr)

// 	// Spend By MultiSig
// 	spendAddr, err := userTapClient.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
// 		AssetId: assetId,
// 		Amt:     uint64(ammt / 2),
// 	})

// 	log.Println("created spend multisig address")

// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	spendRecipients := map[string]uint64{
// 		spendAddr.Encoded: spendAddr.Amount,
// 	}
// 	spendFundResp, err := serverTapClient.wallet.FundVirtualPsbt(
// 		context.TODO(), &assetwalletrpc.FundVirtualPsbtRequest{
// 			Template: &assetwalletrpc.FundVirtualPsbtRequest_Raw{
// 				Raw: &assetwalletrpc.TxTemplate{
// 					Recipients: spendRecipients,
// 				},
// 			},
// 		},
// 	)

// 	log.Println("created spend fund Virtual PSBT")
// 	fundedSpendPkt := deserializeVPacket(spendFundResp.FundedPsbt)
// 	_, userSessionId := userTapClient.partialSignAssetTransfer(fundedSpendPkt,
// 		&arkAssetScript.leaves[0], userScriptKey.RawKey, arkAssetScript.userNonce, serverScriptKey.RawKey.PubKey, arkAssetScript.serverNonce.PubNonce)

// 	log.Println("created asset partial sig for user")

// 	serverPartialSig, _ := serverTapClient.partialSignAssetTransfer(fundedSpendPkt,
// 		&arkAssetScript.leaves[0], serverScriptKey.RawKey, arkAssetScript.serverNonce, userScriptKey.RawKey.PubKey, arkAssetScript.userNonce.PubNonce)

// 	log.Println("created asset partial for server")

// 	transferAssetWitness := userTapClient.combineSigs(userSessionId, serverPartialSig, arkAssetScript.leaves[0], arkAssetScript.tree, arkAssetScript.controlBlock)

// 	// update transferAsset Witnesss
// 	for idx := range fundedSpendPkt.Outputs {
// 		asset := fundedSpendPkt.Outputs[idx].Asset
// 		firstPrevWitness := &asset.PrevWitnesses[0]
// 		if asset.HasSplitCommitmentWitness() {
// 			rootAsset := firstPrevWitness.SplitCommitment.RootAsset
// 			firstPrevWitness = &rootAsset.PrevWitnesses[0]
// 		}
// 		firstPrevWitness.TxWitness = transferAssetWitness
// 	}

// 	vPackets := []*tappsbt.VPacket{fundedSpendPkt}
// 	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	btcTransferPkt, finalizedTransferPackets, _, commitResp := serverTapClient.CommitVirtualPsbts(
// 		transferBtcPkt, vPackets, nil, -1,
// 	)

// 	// sign Btc Transaction
// 	btcControlBlockBytes, err := btcControlBlock.ToBytes()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	assetInputIdx := uint32(0)
// 	serverBtcPartialSig := serverTapClient.partialSignBtcTransfer(
// 		btcTransferPkt, assetInputIdx,
// 		serverInternalKey, btcControlBlockBytes, arkScript.Left,
// 	)
// 	userBtcPartialSig := userTapClient.partialSignBtcTransfer(
// 		btcTransferPkt, assetInputIdx,
// 		userInternalKey, btcControlBlockBytes, arkScript.Left,
// 	)

// 	txWitness := wire.TxWitness{
// 		serverBtcPartialSig,
// 		userBtcPartialSig,
// 		arkScript.Left.Script,
// 		btcControlBlockBytes,
// 	}
// 	var buf bytes.Buffer
// 	err = psbt.WriteTxWitness(&buf, txWitness)
// 	if err != nil {
// 		log.Fatal(err)
// 	}

// 	btcTransferPkt.Inputs[assetInputIdx].FinalScriptWitness = buf.Bytes()
// 	signedPkt := serverTapClient.FinalizePacket(btcTransferPkt)

// 	_ = serverTapClient.LogAndPublish(signedPkt, finalizedTransferPackets, nil,
// 		commitResp,
// 	)

// 	log.Println("Boarding Transaction Complete")

// }
