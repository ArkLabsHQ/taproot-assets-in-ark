package taponark

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

type Round struct {
	id              int32
	transactionTree [][]*wire.MsgTx
}

type Server struct {
	onboardingOutputs []*wire.TxOut
}

func CreateBoardingTapAddres(user, server *btcec.PublicKey, locktime uint32) (*txscript.TapBranch, error) {
	leftLeafScript, err := txscript.NewScriptBuilder().
		AddData(user.SerializeCompressed()).
		AddOp(txscript.OP_CHECKSIGVERIFY).
		AddData(server.SerializeCompressed()).
		Script()

	if err != nil {
		return nil, fmt.Errorf("failed to decode left script: %w", err)
	}

	leftLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, leftLeafScript)

	rightLeafScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(locktime)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(user.SerializeCompressed()).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return nil, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return &branch, nil
}
