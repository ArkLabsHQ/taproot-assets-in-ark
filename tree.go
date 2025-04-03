package taponark

import (
	"bytes"
	"context"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/wire"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/tapsend"
	"github.com/lightningnetwork/lnd/keychain"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
)

// NodeType represents the type of a node in a RoundTree.
type NodeType int

const (
	// NodeTypeBranch indicates a branch node in the tree.
	NodeTypeBranch NodeType = iota
	// NodeTypeLeaf indicates a leaf node in the tree.
	NodeTypeLeaf
)

// OutputType represents the type of output associated with a node.
type OutputType int

const (
	// OutputTypeAsset indicates an asset output.
	OutputTypeAsset OutputType = iota
	// OutputTypeBTC indicates a Bitcoin output.
	OutputTypeBTC
	// OutputTypeColored indicates a colored coin output (if applicable).
	OutputTypeColored
)

// RoundTree represents a tree structure used in a round.
type RoundTree struct {
	Root *RoundTreeNode
}

// RoundTreeNode represents a node within a RoundTree.
type RoundTreeNode struct {
	NodeType    NodeType
	Transaction *wire.MsgTx
	LeftOutput  NodeOutput
	RightOutput NodeOutput
	LeftChild   *RoundTreeNode
	RightChild  *RoundTreeNode
}

// NodeOutput represents the output associated with a node.
type NodeOutput struct {
	OutputType     OutputType
	AssetAmount    uint64
	AssetProof     *proof.Proof
	AssetProofFile []byte
	BTCAmount      int64
}

func ConstructRoundTree(roundTransfer ChainTransfer, assetId []byte, user, server *TapClient, level uint64) (RoundTree, error) {
	var rootNode RoundTreeNode

	roundRootSpendingDetails, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return RoundTree{}, fmt.Errorf("cannot create Round Spending Details %v", err)
	}

	err = constructBranch(assetId, true, roundRootSpendingDetails, roundTransfer, level-1, user, server, &rootNode)
	if err != nil {
		return RoundTree{}, fmt.Errorf("failed to construct branch: %v", err)
	}

	return RoundTree{&rootNode}, nil

}

func constructBranch(assetId []byte, isLeft bool, inputSpendingDetails ArkSpendingDetails, inputChainTransfer ChainTransfer, level uint64, user, server *TapClient, parentNode *RoundTreeNode) error {
	if level == 0 {
		return constructLeaf(assetId, isLeft, inputSpendingDetails, inputChainTransfer, user, server, parentNode)
	}

	leftOutputSpendingDetails, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return fmt.Errorf("failed to create Left output Spending Details %v", err)
	}

	rightOutputSpendingDetail, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return fmt.Errorf("failed to create Right output Spending Details %v", err)
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

	InsertAssetTransferWitness(inputSpendingDetails, fundedPkt, user, server)

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
	leftOutput := NodeOutput{OutputType: OutputTypeColored, AssetProof: leftUnpublishedTransfer.transferProof, BTCAmount: branchBtcAmount, AssetAmount: branchAssetAmount}
	rightOutput := NodeOutput{OutputType: OutputTypeColored, AssetProof: rightUnpublishedTransfer.transferProof, BTCAmount: branchBtcAmount, AssetAmount: branchAssetAmount}

	branchNode := RoundTreeNode{
		NodeType:    NodeTypeBranch,
		Transaction: leftUnpublishedTransfer.finalTx,
		LeftOutput:  leftOutput,
		RightOutput: rightOutput,
	}

	if parentNode == nil {
		parentNode = &branchNode
	} else if isLeft {
		parentNode.LeftChild = &branchNode
	} else {
		parentNode.LeftChild = &branchNode
	}

	leftOutputSpendingDetails.arkBtcScript.controlBlock = leftBtcControlBlock
	rightOutputSpendingDetail.arkBtcScript.controlBlock = rightBtcControlBlock

	// Recursively create the next level of transfers
	err = constructBranch(assetId, true, leftOutputSpendingDetails, leftUnpublishedTransfer, level-1, user, server, parentNode)
	if err != nil {
		return fmt.Errorf("cannot create Intermediate Chain Transfer %v", err)
	}

	err = constructBranch(assetId, false, rightOutputSpendingDetail, rightUnpublishedTransfer, level-1, user, server, parentNode)
	if err != nil {
		return fmt.Errorf("cannot create Intermediate Chain Transfer %v", err)
	}

	return nil
}

func constructLeaf(assetId []byte, isLeft bool, inputSpendingDetails ArkSpendingDetails, inputChainTransfer ChainTransfer, user, server *TapClient, parentNode *RoundTreeNode) error {
	assetOutputIndex := 0
	changeAssetInBtc := DUMMY_ASSET_BTC_AMOUNT
	btcAmount := inputChainTransfer.anchorValue - int64(changeAssetInBtc) - int64(FEE)
	// TODO (Joshua Kindly Improve to only have two output)
	scriptKey, internalKey, err := user.GetNextKeys()
	if err != nil {
		return fmt.Errorf("can get next keys %v", err)
	}

	fundedPkt := tappsbt.ForInteractiveSend(asset.ID(assetId), inputChainTransfer.assetAmount, scriptKey, 0, 0, 0,
		internalKey, asset.V0, &server.tapParams)

	fundedPkt.Outputs[0].Type = tappsbt.TypeSimple

	// Import watch only wallet
	_, err = user.lndClient.wallet.ImportPublicKey(context.TODO(), &walletrpc.ImportPublicKeyRequest{
		PublicKey:   schnorr.SerializePubKey(internalKey.PubKey),
		AddressType: walletrpc.AddressType_TAPROOT_PUBKEY,
	})

	if err != nil {
		return fmt.Errorf("cannot import watch only %v", err)
	}

	// Note: This add input details
	createAndSetInputIntermediate(fundedPkt, inputChainTransfer, assetId)

	// Note: This add output details
	err = tapsend.PrepareOutputAssets(context.TODO(), fundedPkt)
	if err != nil {
		log.Fatalf("cannot prepare Output %v", err)
	}

	InsertAssetTransferWitness(inputSpendingDetails, fundedPkt, user, server)

	vPackets := []*tappsbt.VPacket{fundedPkt}
	transferBtcPkt, err := tapsend.PrepareAnchoringTemplate(vPackets)
	if err != nil {
		return fmt.Errorf("cannot prepare TransferBtc Packet %v", err)
	}
	addBtcOutput(transferBtcPkt, uint64(btcAmount), internalKey.PubKey)

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
	assetVtxo := NodeOutput{OutputType: OutputTypeAsset, AssetProof: unpublishedTransfer.transferProof, AssetAmount: inputChainTransfer.assetAmount}
	btcVtxo := NodeOutput{OutputType: OutputTypeBTC, BTCAmount: btcAmount}
	leafNode := RoundTreeNode{
		Transaction: unpublishedTransfer.finalTx,
		NodeType:    NodeTypeLeaf,
		LeftOutput:  assetVtxo,
		RightOutput: btcVtxo,
	}

	if isLeft {
		parentNode.LeftChild = &leafNode
	} else {
		parentNode.RightChild = &leafNode
	}

	return nil
}
