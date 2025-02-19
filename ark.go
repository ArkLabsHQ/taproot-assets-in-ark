package taponark

import (
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/txscript"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightningnetwork/lnd/input"
	"github.com/lightningnetwork/lnd/keychain"
)

type ArkScript struct {
	Left   txscript.TapLeaf
	Right  txscript.TapLeaf
	Branch txscript.TapBranch
}

type ArkAssetScript struct {
	nonces       []*musig2.Nonces
	tapScriptKey asset.ScriptKey
	leaves       []txscript.TapLeaf
	tree         *txscript.IndexedTapScriptTree
	controlBlock *txscript.ControlBlock
}

type ArkTransferOutputDetails struct {
	btcControlBlock *txscript.ControlBlock

	userScriptKey     asset.ScriptKey
	userInternalKey   keychain.KeyDescriptor
	serverScriptKey   asset.ScriptKey
	serverInternalKey keychain.KeyDescriptor

	arkScript      ArkScript
	arkAssetScript ArkAssetScript

	previousOutput *taprpc.TransferOutput
}

func CreateBoardingArkScript(user, server *btcec.PublicKey, locktime uint32) (ArkScript, error) {
	leftLeafScript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIGADD).
		AddInt64(2).
		AddOp(txscript.OP_EQUAL).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	leftLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, leftLeafScript)

	rightLeafScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(locktime)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return ArkScript{Left: leftLeaf, Right: rightLeaf, Branch: branch}, nil
}

func CreateBoardingArkAssetScript(
	user, server *btcec.PublicKey, locktime uint32) ArkAssetScript {

	userBoardingNonceOpt := musig2.WithPublicKey(
		user,
	)
	serverBoardingNonceOpt := musig2.WithPublicKey(
		server,
	)
	userNonces, _ := musig2.GenNonces(userBoardingNonceOpt)
	serverNonces, _ := musig2.GenNonces(serverBoardingNonceOpt)

	nonces := []*musig2.Nonces{userNonces, serverNonces}

	musigUserServer, err := input.MuSig2CombineKeys(
		input.MuSig2Version100RC2, []*btcec.PublicKey{
			user,
			server,
		}, true, &input.MuSig2Tweaks{TaprootBIP0086Tweak: true},
	)

	if err != nil {
		log.Fatal(err)
	}

	leaves := make([]txscript.TapLeaf, 2)
	muSigTapscript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigUserServer.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatal(err)
	}

	leaves[0] = txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      muSigTapscript,
	}

	unilateralExit, err := txscript.NewScriptBuilder().
		AddInt64(int64(locktime)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatal(err)
	}

	leaves[1] = txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      unilateralExit,
	}

	tree := txscript.AssembleTaprootScriptTree(leaves...)
	internalKey := asset.NUMSPubKey
	controlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: internalKey,
	}
	merkleRootHash := tree.RootNode.TapHash()

	tapKey := txscript.ComputeTaprootOutputKey(
		internalKey, merkleRootHash[:],
	)
	tapScriptKey := asset.ScriptKey{
		PubKey: tapKey,
		TweakedScriptKey: &asset.TweakedScriptKey{
			RawKey: keychain.KeyDescriptor{
				PubKey: internalKey,
			},
			Tweak: merkleRootHash[:],
		},
	}

	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		controlBlock.OutputKeyYIsOdd = true
	}

	return ArkAssetScript{nonces, tapScriptKey, leaves, tree, controlBlock}
}

func CreateRoundLeafArkScript(user, server *btcec.PublicKey, locktime uint32) (ArkScript, error) {
	leftLeafScript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIGADD).
		AddInt64(2).
		AddOp(txscript.OP_EQUAL).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	leftLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, leftLeafScript)

	rightLeafScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(locktime)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return ArkScript{Left: leftLeaf, Right: rightLeaf, Branch: branch}, nil
}

func CreateRoundBrachArkAssetScript(
	server *btcec.PublicKey, locktime uint32, users ...*btcec.PublicKey) ArkAssetScript {
	nonces := make([]*musig2.Nonces, len(users)+1)

	for _, user := range users {
		userBoardingNonceOpt := musig2.WithPublicKey(
			user,
		)

		userNonces, _ := musig2.GenNonces(userBoardingNonceOpt)
		nonces = append(nonces, userNonces)
	}
	serverBoardingNonceOpt := musig2.WithPublicKey(
		server,
	)
	serverNonces, _ := musig2.GenNonces(serverBoardingNonceOpt)
	nonces = append(nonces, serverNonces)

	musigUserServer, err := input.MuSig2CombineKeys(
		input.MuSig2Version100RC2, append(users, server),
		true, &input.MuSig2Tweaks{TaprootBIP0086Tweak: true},
	)

	if err != nil {
		log.Fatal(err)
	}

	leaves := make([]txscript.TapLeaf, 2)
	muSigTapscript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigUserServer.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatal(err)
	}

	leaves[0] = txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      muSigTapscript,
	}

	sweep, err := txscript.NewScriptBuilder().
		AddInt64(int64(locktime)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatal(err)
	}

	leaves[1] = txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      sweep,
	}

	tree := txscript.AssembleTaprootScriptTree(leaves...)
	internalKey := asset.NUMSPubKey
	controlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: internalKey,
	}
	merkleRootHash := tree.RootNode.TapHash()

	tapKey := txscript.ComputeTaprootOutputKey(
		internalKey, merkleRootHash[:],
	)
	tapScriptKey := asset.ScriptKey{
		PubKey: tapKey,
		TweakedScriptKey: &asset.TweakedScriptKey{
			RawKey: keychain.KeyDescriptor{
				PubKey: internalKey,
			},
			Tweak: merkleRootHash[:],
		},
	}

	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		controlBlock.OutputKeyYIsOdd = true
	}

	return ArkAssetScript{nonces, tapScriptKey, leaves, tree, controlBlock}
}

func CreateRoundBranchArkScript(server *btcec.PublicKey, locktime uint32, users ...*btcec.PublicKey) (ArkScript, error) {
	leftLeafScript := txscript.NewScriptBuilder()

	for _, user := range users {
		leftLeafScript.AddData(schnorr.SerializePubKey(user)).
			AddOp(txscript.OP_CHECKSIG)
	}

	leftLeafFinalScript, err := leftLeafScript.
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIGADD).
		AddInt64(int64(len(users))).
		AddOp(txscript.OP_EQUAL).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	leftLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, leftLeafFinalScript)

	rightLeafScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(locktime)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return ArkScript{Left: leftLeaf, Right: rightLeaf, Branch: branch}, nil
}
