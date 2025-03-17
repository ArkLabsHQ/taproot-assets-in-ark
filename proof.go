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
<<<<<<< HEAD
		log.Fatalf("cannot gully encode file %v", err)
=======
		log.Fatalf("cannot encode Proof %v", err)
>>>>>>> d1b355e (Implemented an improved config technique)
	}

	return encodedProofFile

}

func PublishTransfersAndSubmitProofs(assetId []byte, proofList []ProofTxMsg, genesisPoint string, proofFile []byte, user *TapClient, bitcoinClient BitcoinClient) {
	updatedProofList := make([][]byte, 0)

	rootSentMessage := bitcoinClient.SendTransaction(proofList[0].TxMsg)

	processParentAndChild := func(parentSentMessage BitcoinSendTxResult, parent, leftchild ProofTxMsg, proofFile []byte) {
		updatedProof := UpdateAndAppendProof(proofFile, parent.TxMsg, parent.Proof, parentSentMessage)

		sentLeftMessage := bitcoinClient.SendTransaction(leftchild.TxMsg)
		updatedLeftProof := UpdateAndAppendProof(updatedProof, leftchild.TxMsg, leftchild.Proof, sentLeftMessage)

		updatedProofList = append(updatedProofList, updatedLeftProof)
	}

	processParentAndChild(rootSentMessage, proofList[0], proofList[2], proofFile)
	processParentAndChild(rootSentMessage, proofList[1], proofList[3], proofFile)

	log.Println("Exit Proof appended")

	for _, updatedProof := range updatedProofList {
		_, err := user.universeclient.ImportProof(context.TODO(), &tapdevrpc.ImportProofRequest{
			ProofFile:    updatedProof,
			GenesisPoint: genesisPoint,
		})

		if err != nil {
			log.Fatalf("cannot encode proof file %v", err)
		}

	}

	log.Println("Exit Proof Imported")

}
