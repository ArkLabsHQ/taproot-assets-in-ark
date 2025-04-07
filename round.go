package taponark

import (
	"bytes"
	"context"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/tapsend"
	"github.com/lightningnetwork/lnd/keychain"
)

type Round struct {
	roundTransfer          ColoredTransfer
	RoundTree              RoundTree
	assetTransferProofFile []byte
	GenesisPoint           string
}

func ConstructAndBroadcastRound(assetId []byte, onboardTransfer ArkBoardingTransfer, user, server *TapClient, bitcoinClient BitcoinClient) (Round, error) {
	roundSpendingDetails, err := CreateRoundSpendingDetails(user, server)
	if err != nil {
		return Round{}, fmt.Errorf("cannot create Round Spending Details %v", err)
	}

	// Create Asset Transfer
	boardingAssetAmount := onboardTransfer.AssetTransferDetails.assetBoardingAmount
	onboardAssetSpendingDetails := onboardTransfer.AssetTransferDetails.ArkSpendingDetails

	// Prepare an asset Transfer Packet
	assetTransferPkt := tappsbt.ForInteractiveSend(
		asset.ID(assetId),
		boardingAssetAmount,
		roundSpendingDetails.arkAssetScript.tapScriptKey,
		0, 0, 0,
		keychain.KeyDescriptor{
			PubKey: asset.NUMSPubKey,
		},
		asset.V0,
		&server.tapParams)

	// Insert Ark round Spending Script Path
	scriptBranchPreimage := commitment.NewPreimageFromBranch(roundSpendingDetails.arkBtcScript.Branch)
	assetTransferPkt.Outputs[ROUND_ROOT_ASSET_OUTPUT_INDEX].AnchorOutputTapscriptSibling = &scriptBranchPreimage

	// Add asset input details
	insertAssetInputInPacket(assetTransferPkt, ASSET_ANCHOR_ROUND_ROOT_INPUT_INDEX, onboardTransfer.AssetTransferDetails.AssetTransferOutput, assetId)
	err = tapsend.PrepareOutputAssets(context.TODO(), assetTransferPkt)
	if err != nil {
		return Round{}, fmt.Errorf("cannot prepare Output %v", err)
	}
	// Insert asset witness details
	InsertAssetTransferWitness(onboardAssetSpendingDetails, assetTransferPkt, onboardTransfer.user, server)
	assetTransferPktList := []*tappsbt.VPacket{assetTransferPkt}
	transferPsbt, err := tapsend.PrepareAnchoringTemplate(assetTransferPktList)
	if err != nil {
		return Round{}, fmt.Errorf("cannot prepare TransferBtc Packet %v", err)
	}

	//2. Add Boarded Btc Input
	btcAmount := onboardTransfer.btcTransferDetails.btcBoardingAmount + DUMMY_ASSET_BTC_AMOUNT - FEE
	transferPsbt.UnsignedTx.TxOut[ROUND_ROOT_ANCHOR_OUTPUT_INDEX].Value = int64(btcAmount)
	addBtcInputToPSBT(transferPsbt, onboardTransfer.btcTransferDetails)

	// Commit Asset Transfer To Psbt
	server.CommitVirtualPsbts(
		transferPsbt, assetTransferPktList,
	)

	inputLength := 2
	spendingDetailsLists := make([]ArkSpendingDetails, inputLength)
	spendingDetailsLists[0] = onboardTransfer.AssetTransferDetails.ArkSpendingDetails
	spendingDetailsLists[1] = onboardTransfer.btcTransferDetails.arkSpendingDetails

	// Sign BTC inputs
	btcAssetTxWitnessList, err := CreateBtcWitness(spendingDetailsLists, transferPsbt, inputLength, onboardTransfer.user, server)
	if err != nil {
		return Round{}, fmt.Errorf("cannot Create BTC Witness %v", err)
	}

	for i := 0; i < inputLength; i++ {
		var buf bytes.Buffer
		err = psbt.WriteTxWitness(&buf, btcAssetTxWitnessList[i])
		if err != nil {
			return Round{}, fmt.Errorf("failed to write BTC witness for input %v", err)
		}
		transferPsbt.Inputs[i].FinalScriptWitness = buf.Bytes()
	}

	// Finalise Transfer PSBT
	if err = psbt.MaybeFinalizeAll(transferPsbt); err != nil {
		return Round{}, fmt.Errorf("failed to finalise Psbt %v", err)
	}

	roundTransfer, err := ExtractColoredTransfer(transferPsbt, assetTransferPktList[0].Outputs[0])
	if err != nil {
		return Round{}, fmt.Errorf("cannot Derive Unpublished Chain Transfer %v", err)
	}

	// Insert Control Block
	btcControlBlock := extractControlBlock(roundSpendingDetails.arkBtcScript, roundTransfer.taprootAssetRoot)
	roundSpendingDetails.arkBtcScript.controlBlock = btcControlBlock

	// construct a two level roundtree
	roundTree, err := ConstructRoundTree(roundTransfer, roundSpendingDetails, assetId, user, server, 2)
	if err != nil {
		return Round{}, fmt.Errorf("cannot construct round tree, %v", err)
	}

	sendTxResult, err := bitcoinClient.SendTransaction(roundTransfer.finalTx)
	if err != nil {
		return Round{}, fmt.Errorf("failed to broadcast round transaction %v", err)
	}

	rootProofFile, err := AppendProof(onboardTransfer.AssetTransferDetails.RawProofFile, roundTransfer.finalTx, roundTransfer.transferProof, sendTxResult)
	if err != nil {
		return Round{}, fmt.Errorf("failed to update round proof %v", err)
	}

	genesisPoint := onboardTransfer.AssetTransferDetails.GenesisPoint

	log.Printf("\nRound Transaction Hash %s", roundTransfer.finalTx.TxHash().String())

	return Round{
		roundTransfer,
		roundTree,
		rootProofFile,
		genesisPoint,
	}, nil
}

func ExitRoundAndAppendProof(round Round, bitcoinClient *BitcoinClient) ([][]byte, error) {
	assetVtxoProofList := make([][]byte, 0)

	var traverseRecursively func(node *RoundTreeNode, parentProofFile []byte) error

	traverseRecursively = func(node *RoundTreeNode, parentProofFile []byte) error {
		sendTransactionResult, err := bitcoinClient.SendTransaction(node.Transaction)
		if err != nil {
			return fmt.Errorf("failed to broadcast exit  transaction: %w", err)
		}

		if node.NodeType == NodeTypeLeaf {
			for _, output := range []NodeOutput{node.LeftOutput, node.RightOutput} {
				if output.OutputType == OutputTypeAsset {
					if parentProofFile == nil {
						return fmt.Errorf("parent proof file is nil for leaf node")
					}
					assetProofFile, err := AppendProof(parentProofFile, node.Transaction, output.AssetProof, sendTransactionResult)
					if err != nil {
						return err
					}
					assetVtxoProofList = append(assetVtxoProofList, assetProofFile)
				}
			}
			return nil
		}
		for _, output := range []NodeOutput{node.LeftOutput, node.RightOutput} {
			if output.OutputType == OutputTypeColored {
				if parentProofFile == nil {
					return fmt.Errorf("parent proof file is nil for leaf node")
				}
				assetProofFile, err := AppendProof(parentProofFile, node.Transaction, output.AssetProof, sendTransactionResult)
				if err != nil {
					return err
				}
				err = traverseRecursively(output.Node, assetProofFile)
				if err != nil {
					return fmt.Errorf("failed to traverse branch transaction: %w", err)
				}
			} else {
				err = traverseRecursively(output.Node, nil)
				if err != nil {
					return fmt.Errorf("failed to traverse branch transaction: %w", err)
				}
			}

		}
		return nil

	}

	err := traverseRecursively(round.RoundTree.Root, round.assetTransferProofFile)
	if err != nil {
		return nil, fmt.Errorf("failed to traverse round tree: %w", err)
	}

	return assetVtxoProofList, nil

}
