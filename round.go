package taponark

import (
	"bytes"
	"log"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/tapsend"
	"github.com/lightningnetwork/lnd/keychain"
)

type ArkRoundTransferOutputDetails struct {
	btcControlBlock *txscript.ControlBlock

	userScriptKey     asset.ScriptKey
	userInternalKey   keychain.KeyDescriptor
	serverScriptKey   asset.ScriptKey
	serverInternalKey keychain.KeyDescriptor

	arkScript      RoundArkScript
	arkAssetScript RoundArkAssetScript

	assetTransfer *taprpc.AssetTransfer
}

func createAndSignOffchainRoundTransfer(assetDetails ProofOutputDetail, amnt uint64, userTapClient, serverTapClient *TapClient, lockHeight uint32, ato ArkBoardingTransferOutputDetails) ArkRoundTransferOutputDetails {
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

	proofs := make([]*proof.Proof, 1)
	proofs[0] = assetDetails.transferPacket.Outputs[0].ProofSuffix
	fundedPkt, err := tappsbt.FromProofs(proofs, &address.RegressionNetTap)

	// TODO (Joshua) Ensure that correct keys are embedded
	vOut := &tappsbt.VOutput{
		AssetVersion:      asset.Version(assetDetails.assetTransfer.Outputs[0].AssetVersion),
		Type:              0,
		Amount:            amnt,
		Interactive:       true,
		AnchorOutputIndex: 0,
		ScriptKey:         arkAssetScript.tapScriptKey,
	}

	fundedPkt.Outputs = append(fundedPkt.Outputs, vOut)

	_, userSessionId := userTapClient.partialSignAssetTransfer(fundedPkt,
		&ato.arkAssetScript.leaves[0], ato.userScriptKey.RawKey, ato.arkAssetScript.userNonce, ato.serverScriptKey.RawKey.PubKey, arkAssetScript.serverNonce.PubNonce)

	log.Println("created asset partial sig for user")

	serverPartialSig, _ := serverTapClient.partialSignAssetTransfer(fundedPkt,
		&ato.arkAssetScript.leaves[0], ato.serverScriptKey.RawKey, ato.arkAssetScript.serverNonce, ato.userScriptKey.RawKey.PubKey, ato.arkAssetScript.userNonce.PubNonce)

	log.Println("created asset partial for server")

	transferAssetWitness := userTapClient.combineSigs(userSessionId, serverPartialSig, ato.arkAssetScript.leaves[0], ato.arkAssetScript.tree, ato.arkAssetScript.controlBlock)

	// update transferAsset Witnesss
	for idx := range fundedPkt.Outputs {
		asset := fundedPkt.Outputs[idx].Asset
		firstPrevWitness := &asset.PrevWitnesses[0]
		if asset.HasSplitCommitmentWitness() {
			rootAsset := firstPrevWitness.SplitCommitment.RootAsset
			firstPrevWitness = &rootAsset.PrevWitnesses[0]
		}
		firstPrevWitness.TxWitness = transferAssetWitness
	}

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
	multiSigOutAnchor := logAndPublishResponse.Transfer.Outputs[1].Anchor
	rightNodeHash := arkScript.Right.TapHash()
	inclusionproof := append(rightNodeHash[:], multiSigOutAnchor.TaprootAssetRoot[:]...)
	btcControlBlock.InclusionProof = inclusionproof
	rootHash := btcControlBlock.RootHash(arkScript.Left.Script)
	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		btcControlBlock.OutputKeyYIsOdd = true
	}

	return ArkRoundTransferOutputDetails{btcControlBlock, userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript, logAndPublishResponse.Transfer}

}
