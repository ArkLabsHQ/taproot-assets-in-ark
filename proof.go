package taponark

import (
	"context"
	"log"

	"github.com/btcsuite/btcd/wire"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/taprpc/tapdevrpc"
)

func UpdateAndAppendProof(proofFile []byte, finalTx *wire.MsgTx, transferProof *proof.Proof, sendTxResult BitcoinSendTxResult) []byte {
	decodedFullProofFile, err := proof.DecodeFile(proofFile)
	if err != nil {
		log.Fatalf("cannot fully decode proof file %v", err)
	}

	proofParams := proof.BaseProofParams{
		Block:       sendTxResult.block,
		Tx:          finalTx,
		BlockHeight: uint32(sendTxResult.blockHeight),
		TxIndex:     int(1),
	}

	err = transferProof.UpdateTransitionProof(&proofParams)
	if err != nil {
		log.Fatalf("cannot update transfer proof %v", err)
	}

	err = decodedFullProofFile.AppendProof(*transferProof)
	if err != nil {
		log.Fatalf("cannot fully append proof file %v", err)
	}

	encodedProofFile, err := proof.EncodeFile(decodedFullProofFile)
	if err != nil {
		log.Fatalf("cannot encode Proof %v", err)
	}

	return encodedProofFile

}

func PublishTransfersAndSubmitProofs(assetId []byte, vtxoList []VirtualTxOut, genesisPoint string, rootProof []byte, user *TapClient, bitcoinClient *BitcoinClient) {
	updatedProofList := make([][]byte, 0)

	rootSentMessage := bitcoinClient.SendTransaction(vtxoList[0].TxMsg)

	processParentAndChild := func(parentSentMessage BitcoinSendTxResult, parent, leftchild VirtualTxOut, proofFile []byte) {
		updatedProof := UpdateAndAppendProof(proofFile, parent.TxMsg, parent.AssetProof, parentSentMessage)

		sentLeftMessage := bitcoinClient.SendTransaction(leftchild.TxMsg)
		updatedLeftProof := UpdateAndAppendProof(updatedProof, leftchild.TxMsg, leftchild.AssetProof, sentLeftMessage)

		updatedProofList = append(updatedProofList, updatedLeftProof)
	}

	processParentAndChild(rootSentMessage, vtxoList[0], vtxoList[2], rootProof)
	processParentAndChild(rootSentMessage, vtxoList[1], vtxoList[4], rootProof)

	log.Println("Exit Proof appended")

	for _, updatedProof := range updatedProofList {
		_, err := user.devclient.ImportProof(context.TODO(), &tapdevrpc.ImportProofRequest{
			ProofFile:    updatedProof,
			GenesisPoint: genesisPoint,
		})

		if err != nil {
			log.Fatalf("cannot encode proof file %v", err)
		}

	}

	log.Println("Exit Proof Imported")

}
