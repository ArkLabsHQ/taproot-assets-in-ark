package taponark

import (
	"bytes"
	"context"
	"log"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/taprpc/assetwalletrpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
)

type InputRoundDetails struct {
	anchorPoint       wire.OutPoint
	assetId           asset.ID
	anchorScript      []byte
	anchorMerkleRoot  chainhash.Hash
	anchorAmount      btcutil.Amount
	tapscriptSiblings []byte
	proof             *proof.Proof
	scriptKey         asset.ScriptKey
	asset             *asset.Asset
	inputcommitment   tappsbt.InputCommitments
}

type IntermediateOutput struct {
	signedPkt                *psbt.Packet
	finalizedTransferPackets []*tappsbt.VPacket
	commitResp               *assetwalletrpc.CommitVirtualPsbtsResponse
	addr                     *taprpc.Addr
}

func CreateAndSignOffchainIntermediateRoundTransfer(amnt uint64, assetId []byte, userTapClient, serverTapClient *TapClient, lockHeight uint32, ato ArkTransferOutputDetails) (ArkTransferOutputDetails, IntermediateOutput) {
	userScriptKey, userInternalKey := userTapClient.GetNextKeys()
	serverScriptKey, serverInternalKey := serverTapClient.GetNextKeys()

	// 1. Create Both Bording Ark Script and Boarding Ark Asset Script
	arkScript, err := CreateRoundArkScript(userInternalKey.PubKey, serverInternalKey.PubKey, lockHeight)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateRoundArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey, lockHeight)
	if err != nil {
		log.Fatal(err)
	}

	addr_resp, err := serverTapClient.GetBoardingAddress(arkScript.Branch, arkAssetScript.tapScriptKey, assetId, amnt)
	if err != nil {
		log.Fatalf("cannot get address %w", err)
	}
	addr, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %w", err)
	}
	addresses := make([]*address.Tap, 1)
	addresses[0] = addr

	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, 1)

	if err != nil {
		log.Fatalf("cannot generate packet from address %w", err)
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

	logAndPublishResponse := serverTapClient.LogAndPublish(signedPkt, finalizedTransferPackets, nil,
		commitResp,
	)

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

	return ArkTransferOutputDetails{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript, transferOutput}, IntermediateOutput{signedPkt, finalizedTransferPackets, commitResp, addr_resp}

}

func CreateAndSignOffchainFinalRoundTransfer(amnt uint64, assetId []byte, userTapClient, serverTapClient *TapClient, lockHeight uint32, ato ArkTransferOutputDetails, inte IntermediateOutput) {

	addr_resp, err := userTapClient.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId: assetId,
		Amt:     amnt,
	})
	if err != nil {
		log.Fatalf("cannot get address %w", err)
	}
	addr, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %w", err)
	}
	addresses := make([]*address.Tap, 1)
	addresses[0] = addr

	// Note: This create a VPacket
	fundedPkt, err := tappsbt.FromAddresses(addresses, 1)

	if err != nil {
		log.Fatalf("cannot generate packet from address %w", err)
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

	log.Println("Intermediate Asset Transfered Please Mine")
	_ = serverTapClient.LogAndPublish(inte.signedPkt, inte.finalizedTransferPackets, nil,
		inte.commitResp,
	)
	serverTapClient.IncomingTransferEvent(inte.addr)

	log.Println("Final Asset Transfered Please Mine")
	_ = serverTapClient.LogAndPublish(signedPkt, finalizedTransferPackets, nil,
		commitResp,
	)

	userTapClient.IncomingTransferEvent(addr_resp)

}
