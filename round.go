package taponark

import (
	"bytes"
	"context"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/taprpc/tapdevrpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
)

type UnpulishedTransfer struct {
	finalTx          *wire.MsgTx
	outpoint         *wire.OutPoint
	transferProof    *proof.Proof
	changeProof      *proof.Proof
	merkleRoot       []byte
	taprootSibling   []byte
	internalKey      *btcec.PublicKey
	scriptKey        asset.ScriptKey
	anchorValue      int64
	taprootAssetRoot []byte
}

func DeriveUnpublishedOutput(btcPacket *psbt.Packet, vout *tappsbt.VOutput, change *tappsbt.VOutput) UnpulishedTransfer {

	proof := vout.ProofSuffix
	internalKey := vout.AnchorOutputInternalKey
	scriptKey := vout.ScriptKey
	merkleRoot := tappsbt.ExtractCustomField(
		btcPacket.Outputs[vout.AnchorOutputIndex].Unknowns, tappsbt.PsbtKeyTypeOutputTaprootMerkleRoot,
	)
	taprootAssetRoot := tappsbt.ExtractCustomField(
		btcPacket.Outputs[vout.AnchorOutputIndex].Unknowns, tappsbt.PsbtKeyTypeOutputAssetRoot,
	)
	taprootSibling, _, err := commitment.MaybeEncodeTapscriptPreimage(vout.AnchorOutputTapscriptSibling)
	if err != nil {
		log.Fatalf("cannot encode tapscript preimage %v", err)
	}

	finalTx, err := psbt.Extract(btcPacket)
	if err != nil {
		log.Fatalf("cannot extract final transaction %v", err)
	}
	txhash := vout.ProofSuffix.AnchorTx.TxHash()

	outpoint := wire.NewOutPoint(&txhash, vout.AnchorOutputIndex)

	anchorValue := btcPacket.UnsignedTx.TxOut[vout.AnchorOutputIndex].Value

	return UnpulishedTransfer{finalTx, outpoint, proof, change.ProofSuffix, merkleRoot, taprootSibling, internalKey, scriptKey, anchorValue, taprootAssetRoot}

}

func CreateAndSignOffchainIntermediateRoundTransfer(amnt uint64, assetId []byte, userTapClient, serverTapClient *TapClient, lockHeight uint32, ato ArkTransferOutputDetails, unpublishedTransfer *UnpulishedTransfer) (ArkTransferOutputDetails, UnpulishedTransfer) {
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
	if ato.previousOutput != nil {
		createAndSetInput(fundedPkt, 0, ato.previousOutput, assetId)
	} else {
		createAndSetInputIntermediate(fundedPkt, 0, *unpublishedTransfer, assetId)
	}

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

	btcTransferPkt, finalizedTransferPackets, _, _ := serverTapClient.CommitVirtualPsbts(
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

	// spend from Boarding Address
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}

	// Real Transfer is always output 1
	unpublishedTransaction := DeriveUnpublishedOutput(signedPkt, finalizedTransferPackets[0].Outputs[1], finalizedTransferPackets[0].Outputs[0])

	log.Println(unpublishedTransaction.taprootAssetRoot)
	rightNodeHash := arkScript.Right.TapHash()
	inclusionproof := append(rightNodeHash[:], unpublishedTransaction.taprootAssetRoot[:]...)
	btcControlBlock.InclusionProof = inclusionproof
	rootHash := btcControlBlock.RootHash(arkScript.Left.Script)
	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		btcControlBlock.OutputKeyYIsOdd = true
	}

	return ArkTransferOutputDetails{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript, nil, ato.addr}, unpublishedTransaction

}

func CreateAndSignOffchainFinalRoundTransfer(amnt uint64, assetId []byte, userTapClient, serverTapClient *TapClient, lockHeight uint32, ato ArkTransferOutputDetails, inte UnpulishedTransfer) {

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
	createAndSetInputIntermediate(fundedPkt, 0, inte, assetId)

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

	// Publish Intermediate Transaction and Mine
	log.Println("Intermediate Asset Transfered Please Submit and Mine")

	bcoinClient := GetBitcoinClient()
	_, err = bcoinClient.SendRawTransaction(inte.finalTx, true)
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

	intermediateTransferProof := inte.transferProof
	intermediateChangeProof := inte.changeProof
	intermediateAnchorTx := intermediateTransferProof.AnchorTx

	proofParams := proof.BaseProofParams{
		Block:       block,
		Tx:          &intermediateAnchorTx,
		BlockHeight: uint32(blockheight),
		TxIndex:     int(1),
	}

	err = intermediateTransferProof.UpdateTransitionProof(&proofParams)
	if err != nil {
		log.Fatalf("cannot update transfer proof %v", err)
	}

	err = intermediateChangeProof.UpdateTransitionProof(&proofParams)
	if err != nil {
		log.Fatalf("cannot update change proof %v", err)
	}

	fullproof, err := serverTapClient.client.ExportProof(context.TODO(), &taprpc.ExportProofRequest{
		AssetId:   assetId,
		ScriptKey: ato.addr.ScriptKey,
	})

	if err != nil {
		log.Fatalf("cannot get full proof %v", err)
	}

	decodedFullProofFile, err := proof.DecodeFile(fullproof.RawProofFile)
	if err != nil {
		log.Fatalf("cannot fully decode proof file %v", err)
	}

	copyOfDecodedProof, err := proof.DecodeFile(fullproof.RawProofFile)
	if err != nil {
		log.Fatalf("cannot fully decode proof file %v", err)
	}
	err = decodedFullProofFile.AppendProof(*intermediateTransferProof)
	if err != nil {
		log.Fatalf("cannot fully append proof file %v", err)
	}

	err = copyOfDecodedProof.AppendProof(*intermediateChangeProof)
	if err != nil {
		log.Fatalf("cannot fully append proof file %v", err)
	}

	encodedChangeProofFile, err := proof.EncodeFile(copyOfDecodedProof)
	if err != nil {
		log.Fatalf("cannot encode proof File %v", err)
	}

	encodedTransferProofFile, err := proof.EncodeFile(decodedFullProofFile)
	if err != nil {
		log.Fatalf("cannot encode proof File %v", err)
	}

	_, err = serverTapClient.universeclient.ImportProof(context.TODO(), &tapdevrpc.ImportProofRequest{
		ProofFile:    encodedChangeProofFile,
		GenesisPoint: fullproof.GenesisPoint,
	})

	_, err = serverTapClient.universeclient.ImportProof(context.TODO(), &tapdevrpc.ImportProofRequest{
		ProofFile:    encodedTransferProofFile,
		GenesisPoint: fullproof.GenesisPoint,
	})

	if err != nil {
		log.Fatalf("cannot import proof %v", err)
	}

	// Log and Publish Final Transaction
	log.Println("Final Asset Transfered Please Mine")
	_ = serverTapClient.LogAndPublish(signedPkt, finalizedTransferPackets, nil,
		commitResp,
	)

	//userTapClient.IncomingTransferEvent(addr_resp)

	address1, err = bcoinClient.GetNewAddress("")
	if err != nil {
		log.Fatalf("cannot generate address %v", err)
	}
	maxretries = int64(3)
	_, err = bcoinClient.GenerateToAddress(1, address1, &maxretries)
	if err != nil {
		log.Fatalf("cannot generate to address %v", err)
	}

}
