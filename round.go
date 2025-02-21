package taponark

import (
	"bytes"
	"context"
	"log"

	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/taprpc/assetwalletrpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
	"github.com/lightningnetwork/lnd/keychain"
)

type RoundInput struct {
	server  *TapClient
	users   [4]*TapClient
	assetId []byte

	boardingDetails RoundBoardingDetails
}

type RoundOutput struct {
	roundTx           *wire.MsgTx
	unilateralExitTxs [][]wire.MsgTx
}

type RoundBoardingDetails struct {
	boardingUser            *TapClient
	boardingTransferDetails ArkTransferOutputDetails
	boardingAmount          uint64
}

// func CreateRound(roundInput RoundInput) RoundOutput {
// 	rootArkScript :=

// 	return Round{server, users, assetId, nil, make([][]wire.MsgTx, 0), boardingDetails}
// }

type UnpublishedTransfer struct {
	signedPkt                *psbt.Packet
	finalizedTransferPackets *tappsbt.VPacket
	commitResp               *assetwalletrpc.CommitVirtualPsbtsResponse
	addres                     *[]taprpc.Addr
}

func extractPubNonces(userNonceList []*musig2.Nonces) [][]byte {
	list := make([][]byte, len(userNonceList))
	for _, nonce := range userNonceList {
		list = append(list, nonce.PubNonce[:])
	}
	return list
}

func deriveAssetOutput(){
	merkleRoot := tappsbt.ExtractCustomField(
		anchorOut.Unknowns, tappsbt.PsbtKeyTypeOutputTaprootMerkleRoot,
	)
	taprootAssetRoot := tappsbt.ExtractCustomField(
		anchorOut.Unknowns, tappsbt.PsbtKeyTypeOutputAssetRoot,
	)
}

func deriveBtcControlBlock(packet *tappsbt.VOutput, arkScript ArkScript) {
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}

	// Transfer Of Round is always 0
	transferOutput := logAndPublishResponse.Transfer.Outputs[1]
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
}

func SignAssetTransfer(ato ArkTransferOutputDetails, fundedPkt *tappsbt.VPacket) wire.TxWitness {
	userPartialSigList := make([][]byte, len(ato.userTapClientList))
	for index, userTapClient := range ato.userTapClientList {
		userNonceList := ato.arkAssetScript.userNonces
		otherNonces := extractPubNonces(append(userNonceList[:index], userNonceList[index+1:]...))
		otherNonces = append(otherNonces, ato.arkAssetScript.serverNonce.PubNonce[:])
		userPartialSig, _ := userTapClient.partialSignAssetTransfer(fundedPkt,
			&ato.arkAssetScript.leaves[0], ato.userScriptKey[index].RawKey, userNonceList[index], ato.serverScriptKey.RawKey.PubKey, otherNonces)
		userPartialSigList = append(userPartialSigList, userPartialSig)
	}

	log.Println("created asset partial sig for user")
	otherNonces := extractPubNonces(ato.arkAssetScript.userNonces)
	_, serverSessionId := ato.serverTapClient.partialSignAssetTransfer(fundedPkt,
		&ato.arkAssetScript.leaves[0], ato.serverScriptKey.RawKey, ato.arkAssetScript.serverNonce, ato.userScriptKey.RawKey.PubKey, otherNonces)

	log.Println("created asset partial for server")

	transferAssetWitness := ato.serverTapClient.combineSigs(serverSessionId, userPartialSigList, ato.arkAssetScript.leaves[0], ato.arkAssetScript.tree, ato.arkAssetScript.controlBlock)
	log.Println("Asset Transfer All signed")
	return transferAssetWitness
}

func SignBtcAssetTransfer(ato ArkTransferOutputDetails, btcPacket *psbt.Packet) wire.TxWitness {
	btcControlBlockBytes, err := ato.btcControlBlock.ToBytes()
	if err != nil {
		log.Fatal(err)
	}
	assetInputIdx := uint32(0)
	serverBtcPartialSig := ato.serverTapClient.partialSignBtcTransfer(
		btcTransferPkt, assetInputIdx,
		ato.serverInternalKey, btcControlBlockBytes, ato.arkScript.Left,
	)

	txWitness := wire.TxWitness{
		serverBtcPartialSig,
	}

	for index, userTapClient := range ato.userTapClientList {
		userBtcPartialSig := userTapClient.partialSignBtcTransfer(
			btcTransferPkt, assetInputIdx,
			ato.userInternalKey[index], btcControlBlockBytes, ato.arkScript.Left,
		)
		txWitness = append(txWitness, userBtcPartialSig)
	}
	

	txWitness := append(txWitness,
		ato.arkScript.Left.Script,
		btcControlBlockBytes,
	)

	return txWitness
}

func CreateAndSignOffchainIntermediateRoundTransfer(amnt uint64, assetId []byte, usersTapClient []*TapClient, serverTapClient *TapClient, lockHeight uint32, ato ArkTransferOutputDetails, txtree *[]UnpublishedTransfer) (ArkTransferOutputDetails, UnpublishedTransfer) {

	if len(usersTapClient) == 1 {
		unpublishedTransfer := createAndSignOffchainFinalRoundTransfer(amnt, assetId, usersTapClient[0], serverTapClient, lockHeight, ato)
	}

	mid := len(usersTapClient) / 2 // since usersTapClient is even, mid is an integer
	leftTapClients := usersTapClient[:mid]
	rightTapClients := usersTapClient[mid:]

	deriveKeys := func(clients []*TapClient) ([]keychain.KeyDescriptor, []asset.ScriptKey) {
		userInternalKeys := make([]keychain.KeyDescriptor, len(usersTapClient))
		userScriptKeys := make([]asset.ScriptKey, len(usersTapClient))

		for _, userTapClient := range usersTapClient {
			userScriptKey, userInternalKey := userTapClient.GetNextKeys()

			userInternalKeys = append(userInternalKeys, userInternalKey)
			userScriptKeys = append(userScriptKeys, userScriptKey)
		}

		return userInternalKeys, userScriptKeys
	}

	serverScriptKey, serverInternalKey := serverTapClient.GetNextKeys()

	// derive Left Branch Details
	leftUserInternalKeys, leftUserAssetKeys := deriveKeys(leftTapClients)
	leftArkScript, err := CreateRoundBranchArkScript(serverInternalKey.PubKey, lockHeight, leftUserInternalKeys...)
	if err != nil {
		log.Fatal(err)
	}
	leftArkAssetScript := CreateRoundBranchArkAssetScript(serverScriptKey.RawKey.PubKey, lockHeight, leftUserAssetKeys...)
	if err != nil {
		log.Fatal(err)
	}
	leftAddrResp, err := serverTapClient.GetBoardingAddress(leftArkScript.Branch, leftArkAssetScript.tapScriptKey, assetId, amnt/2)
	if err != nil {
		log.Fatalf("cannot get address %v", err)
	}
	leftAddr, err := address.DecodeAddress(leftAddrResp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.

	// derive Right Branch Details
	rightUserInternalKeys, rightUserAssetKeys := deriveKeys(rightTapClients)
	rightArkScript, err := CreateRoundBranchArkScript(serverInternalKey.PubKey, lockHeight, rightUserInternalKeys...)
	if err != nil {
		log.Fatal(err)
	}
	rightArkAssetScript := CreateRoundBranchArkAssetScript(serverScriptKey.RawKey.PubKey, lockHeight, rightUserAssetKeys...)
	if err != nil {
		log.Fatal(err)
	}
	rightAddrResp, err := serverTapClient.GetBoardingAddress(rightArkScript.Branch, rightArkAssetScript.tapScriptKey, assetId, amnt/2)
	if err != nil {
		log.Fatalf("cannot get address %v", err)
	}
	rightAddr, err := address.DecodeAddress(rightAddrResp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	addresses := []*address.Tap{leftAddr, rightAddr}

	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, 1)

	if err != nil {
		log.Fatalf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInput(fundedPkt, ato.outputIndex, ato.previousOutput, assetId)

	// Note: This add output details
	tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	// Ensure To Sign Asset Transfer Transaction
	transferAssetWitness := SignAssetTransfer(ato, fundedPkt)

	// update transferAsset Witnesss [Nothing Needs To Change]
	for idx := range fundedPkt.Outputs {
		asset := fundedPkt.Outputs[idx].Asset
		firstPrevWitness := &asset.PrevWitnesses[0]
		if asset.HasSplitCommitmentWitness() {
			rootAsset := firstPrevWitness.SplitCommitment.RootAsset
			firstPrevWitness = &rootAsset.PrevWitnesses[0]
		}
		firstPrevWitness.TxWitness = transferAssetWitness
	}
	// ensure the split asset root  have an anchor output
	changeOutput := fundedPkt.Outputs[0]
	changeOutput.AnchorOutputInternalKey = asset.NUMSPubKey
	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt, finalizedTransferPackets, _, commitResp := serverTapClient.CommitVirtualPsbts(
		transferBtcPkt, vPackets, nil, -1,
	)

	// sign Btc Transaction
	transferBtcAssetWitness := SignBtcAssetTransfer(ato, transferBtcPkt)
	
	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, transferBtcAssetWitness)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt.Inputs[assetInputIdx].FinalScriptWitness = buf.Bytes()
	signedPkt := serverTapClient.FinalizePacket(btcTransferPkt)

	unpublishedTransfer :=  UnpublishedTransfer { signedPkt, finalizedTransferPackets[0], commitResp, []*taprpc.Addr {leftAddr, rightAddr}}


	// spend from Boarding Address
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}

	// Transfer Of Round is always 0
	transferOutput := logAndPublishResponse.Transfer.Outputs[1]
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

	return ArkTransferOutputDetails{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript, transferOutput}, UnpublishedTransfer{signedPkt, finalizedTransferPackets, commitResp, addr_resp}

}

func createAndSignOffchainFinalRoundTransfer(amnt uint64, assetId []byte, userTapClient, serverTapClient *TapClient, lockHeight uint32, ato ArkTransferOutputDetails) UnpublishedTransfer {

	addr_resp, err := userTapClient.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId: assetId,
		Amt:     amnt,
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
	fundedPkt, err := tappsbt.FromAddresses(addresses, 1)

	if err != nil {
		log.Fatalf("cannot generate packet from address %v", err)
	}

	// Note: This add input details
	createAndSetInput(fundedPkt, 0, ato.previousOutput, assetId)

	// Note: This add output details
	tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)

	_, userSessionId := userTapClient.partialSignAssetTransfer(fundedPkt,
		&ato.arkAssetScript.leaves[0], ato.userScriptKey.RawKey, ato.arkAssetScript.userNonce, ato.serverScriptKey.RawKey.PubKey, ato.arkAssetScript.serverNonce.PubNonce)

	log.Println("created asset partial sig for user")

	serverPartialSig, _ := serverTapClient.partialSignAssetTransfer(fundedPkt,
		&ato.arkAssetScript.leaves[0], ato.serverScriptKey.RawKey, ato.arkAssetScript.serverNonce, ato.userScriptKey.RawKey.PubKey, ato.arkAssetScript.userNonce.PubNonce)

	log.Println("created asset partial for server")

	transferAssetWitness := userTapClient.combineSigs(userSessionId, serverPartialSig, ato.arkAssetScript.leaves[0], ato.arkAssetScript.tree, ato.arkAssetScript.controlBlock)

	// update transferAsset Witnesss [Nothing Needs To Change]
	for idx := range fundedPkt.Outputs {
		asset := fundedPkt.Outputs[idx].Asset
		firstPrevWitness := &asset.PrevWitnesses[0]
		if asset.HasSplitCommitmentWitness() {
			rootAsset := firstPrevWitness.SplitCommitment.RootAsset
			firstPrevWitness = &rootAsset.PrevWitnesses[0]
		}
		firstPrevWitness.TxWitness = transferAssetWitness
	}
	// ensure the split asset root  have an anchor output
	changeOutput := fundedPkt.Outputs[0]
	changeOutput.AnchorOutputInternalKey = asset.NUMSPubKey
	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt, finalizedTransferPackets, _, commitResp := serverTapClient.CommitVirtualPsbts(
		transferBtcPkt, vPackets, nil, -1,
	)

	// sign Btc Transaction
	btcControlBlockBytes, err := ato.btcControlBlock.ToBytes()
	if err != nil {
		log.Fatal(err)
	}
	assetInputIdx := uint32(0)
	serverBtcPartialSig := serverTapClient.partialSignBtcTransfer(
		btcTransferPkt, assetInputIdx,
		ato.serverInternalKey, btcControlBlockBytes, ato.arkScript.Left,
	)
	userBtcPartialSig := userTapClient.partialSignBtcTransfer(
		btcTransferPkt, assetInputIdx,
		ato.userInternalKey, btcControlBlockBytes, ato.arkScript.Left,
	)

	txWitness := wire.TxWitness{
		serverBtcPartialSig,
		userBtcPartialSig,
		ato.arkScript.Left.Script,
		btcControlBlockBytes,
	}
	var buf bytes.Buffer
	err = psbt.WriteTxWitness(&buf, txWitness)
	if err != nil {
		log.Fatal(err)
	}

	btcTransferPkt.Inputs[assetInputIdx].FinalScriptWitness = buf.Bytes()
	signedPkt := serverTapClient.FinalizePacket(btcTransferPkt)

	log.Println("Final Asset Transfered Please Mine")
	_ = serverTapClient.LogAndPublish(signedPkt, finalizedTransferPackets, nil,
		commitResp,
	)

	return UnpublishedTransfer{signedPkt, finalizedTransferPackets, addr_resp}

}
