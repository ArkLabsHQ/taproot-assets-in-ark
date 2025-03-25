package taponark

import (
	"context"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/wire"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/taprpc/tapdevrpc"
)

func UpdateAndAppendProof(proofFile []byte, finalTx *wire.MsgTx, transferProof *proof.Proof, sendTxResult BitcoinSendTxResult) ([]byte, error) {
	decodedFullProofFile, err := proof.DecodeFile(proofFile)
	if err != nil {
		return nil, fmt.Errorf("cannot fully decode proof file %v", err)
	}

	proofParams := proof.BaseProofParams{
		Block:       sendTxResult.block,
		Tx:          finalTx,
		BlockHeight: uint32(sendTxResult.blockHeight),
		TxIndex:     int(1),
	}

	err = transferProof.UpdateTransitionProof(&proofParams)
	if err != nil {
		return nil, fmt.Errorf("cannot update transfer proof %v", err)
	}

	err = decodedFullProofFile.AppendProof(*transferProof)
	if err != nil {
		return nil, fmt.Errorf("cannot fully append proof file %v", err)
	}

	encodedProofFile, err := proof.EncodeFile(decodedFullProofFile)
	if err != nil {
		return nil, fmt.Errorf("cannot encode Proof %v", err)
	}

	return encodedProofFile, nil

}

func PublishTransfersAndSubmitProofs(assetId []byte, vtxoList []VirtualTxOut, genesisPoint string, rootProof []byte, user *TapClient, bitcoinClient *BitcoinClient) error {
	updatedProofList := make([][]byte, 0)

	rootSentMessage, err := bitcoinClient.SendTransaction(vtxoList[0].TxMsg)
	if err != nil {
		return fmt.Errorf("cannot send transaction %v", err)
	}

	processParentAndChild := func(parentSentMessage BitcoinSendTxResult, parent, leftchild VirtualTxOut, proofFile []byte) error {
		updatedProof, err := UpdateAndAppendProof(proofFile, parent.TxMsg, parent.AssetProof, parentSentMessage)
		if err != nil {
			return fmt.Errorf("cannot update proofs %v", err)
		}

		sentLeftMessage, err := bitcoinClient.SendTransaction(leftchild.TxMsg)
		if err != nil {
			return fmt.Errorf("cannot send transaction %v", err)
		}

		updatedLeftProof, err := UpdateAndAppendProof(updatedProof, leftchild.TxMsg, leftchild.AssetProof, sentLeftMessage)
		if err != nil {
			return fmt.Errorf("cannot update proofs %v", err)
		}

		updatedProofList = append(updatedProofList, updatedLeftProof)

		return nil
	}

	err = processParentAndChild(rootSentMessage, vtxoList[0], vtxoList[2], rootProof)
	if err != nil {
		return err
	}

	err = processParentAndChild(rootSentMessage, vtxoList[1], vtxoList[4], rootProof)
	if err != nil {
		return err
	}

	log.Println("Exit Proof appended")

	for _, updatedProof := range updatedProofList {
		_, err := user.devclient.ImportProof(context.TODO(), &tapdevrpc.ImportProofRequest{
			ProofFile:    updatedProof,
			GenesisPoint: genesisPoint,
		})

		if err != nil {
			return fmt.Errorf("cannot encode proof file %v", err)
		}

	}

	log.Println("Exit Proof Imported")

	return nil
}
