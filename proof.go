package taponark

import (
	"context"
	"fmt"

	"github.com/btcsuite/btcd/wire"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/taprpc/tapdevrpc"
)

func AppendProof(proofFile []byte, finalTx *wire.MsgTx, transferProof *proof.Proof, sendTxResult BitcoinSendTxResult) ([]byte, error) {
	decodedFullProofFile, err := proof.DecodeFile(proofFile)
	if err != nil {
		return nil, fmt.Errorf("cannot fully decode proof file %v", err)
	}

	proofParams := proof.BaseProofParams{
		Block:       sendTxResult.block,
		Tx:          finalTx,
		BlockHeight: uint32(sendTxResult.blockHeight),
		TxIndex:     sendTxResult.txindex,
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

func SubmitProof(genesisPoint string, proofFile []byte, user *TapClient) error {
	_, err := user.devclient.ImportProof(context.TODO(), &tapdevrpc.ImportProofRequest{
		ProofFile:    proofFile,
		GenesisPoint: genesisPoint,
	})
	return err
}
