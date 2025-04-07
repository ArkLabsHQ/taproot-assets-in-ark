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
}

// NodeOutput represents the output associated with a node.
type NodeOutput struct {
	OutputType  OutputType
	AssetAmount uint64
	AssetProof  *proof.Proof
	BTCAmount   int64
	Node        *RoundTreeNode
}

// Access left child
func (n *RoundTreeNode) LeftChild() *RoundTreeNode {
	return n.LeftOutput.Node
}

// Access right child
func (n *RoundTreeNode) RightChild() *RoundTreeNode {
	return n.RightOutput.Node
}

// Display content depending if branch or leaf
func (n *RoundTreeNode) Display() string {
	txid := n.Transaction.TxHash().String()

	if n.NodeType == NodeTypeLeaf {
		vtxoDisplay := make([]string, 2)
		for index, output := range []NodeOutput{n.LeftOutput, n.RightOutput} {
			if output.OutputType == OutputTypeAsset {
				vtxoDisplay[index] = fmt.Sprintf("Token Vtxo [%d]", output.AssetAmount)
			} else {
				vtxoDisplay[index] = fmt.Sprintf("BTC Vtxo [%d]", output.BTCAmount)
			}
		}
		return fmt.Sprintf("(%s, %s, %s)", txid, vtxoDisplay[0], vtxoDisplay[1])
	}

	totalAsset := n.LeftOutput.AssetAmount + n.RightOutput.AssetAmount
	totalBTC := n.LeftOutput.BTCAmount + n.RightOutput.BTCAmount
	return fmt.Sprintf("(%s, Token %d, BTC %d)", txid, totalAsset, totalBTC)

}

func PrintTree(node *RoundTreeNode, prefix string, isTail bool) {
	if node == nil {
		return
	}

	// Print current node
	fmt.Println(prefix + "└── " + node.Display())

	// Collect children
	children := []*RoundTreeNode{}
	if node.LeftChild() != nil {
		children = append(children, node.LeftChild())
	}
	if node.RightChild() != nil {
		children = append(children, node.RightChild())
	}

	// Recursively print children
	for i, child := range children {
		isLast := i == len(children)-1
		newPrefix := prefix
		if isTail {
			newPrefix += "    "
		} else {
			newPrefix += "│   "
		}
		PrintTree(child, newPrefix, isLast)
	}
}

func ConstructRoundTree(roundTransfer ColoredTransfer, roundSpendingDetails ArkSpendingDetails, assetId []byte, user, server *TapClient, level uint64) (RoundTree, error) {
	var rootNode *RoundTreeNode

	err := constructBranch(assetId, true, roundSpendingDetails, roundTransfer, level-1, user, server, &rootNode)
	if err != nil {
		return RoundTree{}, fmt.Errorf("failed to construct branch: %v", err)
	}

	return RoundTree{rootNode}, nil

}

func constructBranch(assetId []byte, isLeft bool, inputSpendingDetails ArkSpendingDetails, prevColoredTransfer ColoredTransfer, level uint64, user, server *TapClient, parentNode **RoundTreeNode) error {
	if level == 0 {
		return constructLeaf(assetId, isLeft, inputSpendingDetails, prevColoredTransfer, user, server, parentNode)
	}

	leftOutputSpendingDetails, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return fmt.Errorf("failed to create Left output Spending Details %v", err)
	}

	rightOutputSpendingDetail, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return fmt.Errorf("failed to create Right output Spending Details %v", err)
	}

	branchBtcAmount := (prevColoredTransfer.anchorValue - int64(FEE)) / 2 // Fee
	branchAssetAmount := prevColoredTransfer.assetAmount / 2

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
	createAndSetInputIntermediate(fundedPkt, prevColoredTransfer, assetId)
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
	leftUnpublishedTransfer, err := ExtractColoredTransfer(transferBtcPkt, vPackets[0].Outputs[0])
	if err != nil {
		return fmt.Errorf("cannot derive left Output Colored Transfer %v", err)
	}

	rightUnpublishedTransfer, err := ExtractColoredTransfer(transferBtcPkt, vPackets[0].Outputs[1])
	if err != nil {
		return fmt.Errorf("cannot derive right Output Colored Transfer %v", err)
	}

	// derive Left and Right Control Blocks
	leftBtcControlBlock := extractControlBlock(leftOutputSpendingDetails.arkBtcScript, leftUnpublishedTransfer.taprootAssetRoot)
	rightBtcControlBlock := extractControlBlock(rightOutputSpendingDetail.arkBtcScript, rightUnpublishedTransfer.taprootAssetRoot)

	// derive and  Left and Right Proofs Details to the ProofList
	leftOutput := NodeOutput{OutputType: OutputTypeColored, AssetProof: leftUnpublishedTransfer.transferProof, BTCAmount: branchBtcAmount, AssetAmount: branchAssetAmount}
	rightOutput := NodeOutput{OutputType: OutputTypeColored, AssetProof: rightUnpublishedTransfer.transferProof, BTCAmount: branchBtcAmount, AssetAmount: branchAssetAmount}

	branchNode := &RoundTreeNode{
		NodeType:    NodeTypeBranch,
		Transaction: leftUnpublishedTransfer.finalTx,
		LeftOutput:  leftOutput,
		RightOutput: rightOutput,
	}

	if *parentNode == nil {
		*parentNode = branchNode
	} else if isLeft {
		(*parentNode).LeftOutput.Node = branchNode
	} else {
		(*parentNode).RightOutput.Node = branchNode
	}

	leftOutputSpendingDetails.arkBtcScript.controlBlock = leftBtcControlBlock
	rightOutputSpendingDetail.arkBtcScript.controlBlock = rightBtcControlBlock

	// Recursively create the next level of transfers
	err = constructBranch(assetId, true, leftOutputSpendingDetails, leftUnpublishedTransfer, level-1, user, server, &branchNode)
	if err != nil {
		return fmt.Errorf("cannot construct Left Branch Transaction %v", err)
	}

	err = constructBranch(assetId, false, rightOutputSpendingDetail, rightUnpublishedTransfer, level-1, user, server, &branchNode)
	if err != nil {
		return fmt.Errorf("cannot construct Right Branch Transaction %v", err)
	}

	return nil
}

func constructLeaf(assetId []byte, isLeft bool, inputSpendingDetails ArkSpendingDetails, prevColoredTransfer ColoredTransfer, user, server *TapClient, parentNode **RoundTreeNode) error {
	assetOutputIndex := 0
	changeAssetInBtc := DUMMY_ASSET_BTC_AMOUNT
	btcAmount := prevColoredTransfer.anchorValue - int64(changeAssetInBtc) - int64(FEE)

	scriptKey, internalKey, err := user.GetNextKeys()
	if err != nil {
		return fmt.Errorf("can get next keys %v", err)
	}

	fundedPkt := tappsbt.ForInteractiveSend(asset.ID(assetId), prevColoredTransfer.assetAmount, scriptKey, 0, 0, 0,
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
	createAndSetInputIntermediate(fundedPkt, prevColoredTransfer, assetId)

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
		return fmt.Errorf("failed to finalise Psbt %v", err)
	}

	// derive Asset Unpublished Transfers
	unpublishedTransfer, err := ExtractColoredTransfer(transferBtcPkt, vPackets[0].Outputs[assetOutputIndex])
	if err != nil {
		return fmt.Errorf("cannot Extract Colored Transfer %v", err)
	}
	assetVtxo := NodeOutput{OutputType: OutputTypeAsset, AssetProof: unpublishedTransfer.transferProof, AssetAmount: prevColoredTransfer.assetAmount}
	btcVtxo := NodeOutput{OutputType: OutputTypeBTC, BTCAmount: btcAmount}
	leafNode := RoundTreeNode{
		Transaction: unpublishedTransfer.finalTx,
		NodeType:    NodeTypeLeaf,
		LeftOutput:  assetVtxo,
		RightOutput: btcVtxo,
	}

	if isLeft {
		(*parentNode).LeftOutput.Node = &leafNode
	} else {
		(*parentNode).RightOutput.Node = &leafNode
	}

	return nil
}
