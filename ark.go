package taponark

import (
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightningnetwork/lnd/input"
	"github.com/lightningnetwork/lnd/keychain"
)

const LOCK_BLOCK_HEIGHT = 4320

type ArkBtcScript struct {
	cooperativeSpend txscript.TapLeaf
	unilateralSpend  txscript.TapLeaf
	Branch           txscript.TapBranch
	controlBlock     *txscript.ControlBlock
}

type ArkAssetScript struct {
	userNonce    *musig2.Nonces
	serverNonce  *musig2.Nonces
	tapScriptKey asset.ScriptKey

	cooperativeSpend txscript.TapLeaf
	unilateralSpend  txscript.TapLeaf

	tree         *txscript.IndexedTapScriptTree
	controlBlock *txscript.ControlBlock
}

type ArkSpendingDetails struct {
	userScriptKey     asset.ScriptKey
	userInternalKey   keychain.KeyDescriptor
	serverScriptKey   asset.ScriptKey
	serverInternalKey keychain.KeyDescriptor

	arkBtcScript   ArkBtcScript
	arkAssetScript ArkAssetScript
}

type ArkBtcKeys struct {
	arkScript         ArkBtcScript
	userInternalKey   keychain.KeyDescriptor
	serverInternalKey keychain.KeyDescriptor
}

type ColoredTransfer struct {
	finalTx          *wire.MsgTx
	outpoint         *wire.OutPoint
	transferProof    *proof.Proof
	merkleRoot       []byte
	taprootSibling   []byte
	internalKey      *btcec.PublicKey
	scriptKey        asset.ScriptKey
	anchorValue      int64
	taprootAssetRoot []byte
	assetAmount      uint64
}

type ArkBoardingTransfer struct {
	AssetTransferDetails AssetTransferDetails
	btcTransferDetails   BtcTransferDetails
	user                 *TapClient
}

type AssetTransferDetails struct {
	AssetTransferOutput *taprpc.TransferOutput
	ArkSpendingDetails  ArkSpendingDetails
	assetBoardingAmount uint64
	*taprpc.ProofFile
}

type BtcTransferDetails struct {
	txout              *wire.TxOut
	outpoint           *wire.OutPoint
	btcBoardingAmount  uint64
	arkSpendingDetails ArkSpendingDetails
}

func CreateRoundSpendingDetails(user, server *TapClient) (ArkSpendingDetails, error) {
	userScriptKey, userInternalKey, err := user.GetNextKeys()
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to fetch user  keys %v", err)
	}

	serverScriptKey, serverInternalKey, err := server.GetNextKeys()
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to fetch server keys %v", err)
	}

	arkBtcScript, err := CreateRoundArkBtcScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to fetch round ark btc script %v", err)
	}
	arkAssetScript, err := CreateRoundArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey)
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to fetch round ark asset script %v", err)
	}

	return ArkSpendingDetails{
		userScriptKey,
		userInternalKey,
		serverScriptKey,
		serverInternalKey,
		arkBtcScript,
		arkAssetScript,
	}, nil

}

func CreateOnboardSpendingDetails(user, server *TapClient) (ArkSpendingDetails, error) {
	userScriptKey, userInternalKey, err := user.GetNextKeys()
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to fetch user  keys %v", err)
	}

	serverScriptKey, serverInternalKey, err := server.GetNextKeys()
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to fetch server keys %v", err)
	}

	arkBtcScript, err := CreateBoardingArkBtcScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to create round ark btc script %v", err)
	}
	arkAssetScript, err := CreateBoardingArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey)
	if err != nil {
		return ArkSpendingDetails{}, fmt.Errorf("failed to create round ark asset script %v", err)
	}
	return ArkSpendingDetails{
		userScriptKey,
		userInternalKey,
		serverScriptKey,
		serverInternalKey,
		arkBtcScript,
		arkAssetScript,
	}, nil

}

func CreateBtcKeys(amount uint64, user, server *TapClient) (ArkBtcKeys, error) {
	_, userInternalKey, err := user.GetNextKeys()
	if err != nil {
		return ArkBtcKeys{}, err
	}

	_, serverInternalKey, err := server.GetNextKeys()
	if err != nil {
		return ArkBtcKeys{}, err
	}

	arkScript, err := CreateBoardingArkBtcScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		return ArkBtcKeys{}, err
	}

	return ArkBtcKeys{arkScript, userInternalKey, serverInternalKey}, nil

}

func CreateBoardingArkBtcScript(user, server *btcec.PublicKey) (ArkBtcScript, error) {
	cooperativeScript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIGADD).
		AddInt64(2).
		AddOp(txscript.OP_EQUAL).
		Script()

	if err != nil {
		return ArkBtcScript{}, fmt.Errorf("failed to create cooperative script: %w", err)

	}

	cooperativeLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, cooperativeScript)

	unilateralScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkBtcScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	unilateralLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, unilateralScript)

	branch := txscript.NewTapBranch(cooperativeLeaf, unilateralLeaf)

	return ArkBtcScript{cooperativeSpend: cooperativeLeaf, unilateralSpend: unilateralLeaf, Branch: branch}, nil
}

func CreateBoardingArkAssetScript(user, server *btcec.PublicKey) (ArkAssetScript, error) {
	userBoardingNonceOpt := musig2.WithPublicKey(
		user,
	)
	serverBoardingNonceOpt := musig2.WithPublicKey(
		server,
	)
	userNonces, _ := musig2.GenNonces(userBoardingNonceOpt)
	serverNonces, _ := musig2.GenNonces(serverBoardingNonceOpt)

	musigUserServer, err := input.MuSig2CombineKeys(
		input.MuSig2Version100RC2, []*btcec.PublicKey{
			user,
			server,
		}, true, &input.MuSig2Tweaks{TaprootBIP0086Tweak: true},
	)

	if err != nil {
		return ArkAssetScript{}, fmt.Errorf("failed to combine musig keys: %v", err)
	}

	cooperativeScript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigUserServer.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkAssetScript{}, fmt.Errorf("cannot create tapscript %v", err)
	}

	cooperativeSpend := txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      cooperativeScript,
	}

	sweep, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkAssetScript{}, fmt.Errorf("cannot create sweep script %v", err)
	}

	unilateralExit := txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      sweep,
	}

	tree := txscript.AssembleTaprootScriptTree(cooperativeSpend, unilateralExit)
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

	return ArkAssetScript{userNonces, serverNonces, tapScriptKey, cooperativeSpend, unilateralExit, tree, controlBlock}, nil
}

func CreateRoundArkBtcScript(user, server *btcec.PublicKey) (ArkBtcScript, error) {
	cooperativeScript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIGADD).
		AddInt64(2).
		AddOp(txscript.OP_EQUAL).
		Script()

	if err != nil {
		return ArkBtcScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	cooperativeLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, cooperativeScript)

	unilateralScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkBtcScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	unilateralLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, unilateralScript)

	branch := txscript.NewTapBranch(cooperativeLeaf, unilateralLeaf)

	return ArkBtcScript{cooperativeSpend: cooperativeLeaf, unilateralSpend: unilateralLeaf, Branch: branch}, nil
}

func CreateRoundArkAssetScript(
	user, server *btcec.PublicKey) (ArkAssetScript, error) {

	userBoardingNonceOpt := musig2.WithPublicKey(
		user,
	)
	serverBoardingNonceOpt := musig2.WithPublicKey(
		server,
	)
	userNonces, _ := musig2.GenNonces(userBoardingNonceOpt)
	serverNonces, _ := musig2.GenNonces(serverBoardingNonceOpt)

	musigKey, err := input.MuSig2CombineKeys(
		input.MuSig2Version100RC2, []*btcec.PublicKey{
			user,
			server,
		}, true, &input.MuSig2Tweaks{TaprootBIP0086Tweak: true},
	)

	if err != nil {
		return ArkAssetScript{}, fmt.Errorf("failed to combine sigs for Musig: %v", err)
	}

	cooperativeScript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigKey.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkAssetScript{}, fmt.Errorf("failed to create cooperative Script %v", err)
	}

	cooperativeSpend := txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      cooperativeScript,
	}

	unilateralScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkAssetScript{}, fmt.Errorf("failed to  create sweep Tapscript %v", err)
	}

	unilateralLeaf := txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      unilateralScript,
	}

	tree := txscript.AssembleTaprootScriptTree(cooperativeSpend, unilateralLeaf)
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

	return ArkAssetScript{userNonces, serverNonces, tapScriptKey, cooperativeSpend, unilateralLeaf, tree, controlBlock}, nil
}

func InsertAssetTransferWitness(arkSpendingDetails ArkSpendingDetails, fundedPkt *tappsbt.VPacket, user, server *TapClient) error {
	_, serverSessionId, err := server.partialSignAssetTransfer(fundedPkt,
		&arkSpendingDetails.arkAssetScript.cooperativeSpend, arkSpendingDetails.serverScriptKey.RawKey, arkSpendingDetails.arkAssetScript.serverNonce, arkSpendingDetails.userScriptKey.RawKey.PubKey, arkSpendingDetails.arkAssetScript.userNonce.PubNonce)
	if err != nil {
		return fmt.Errorf("failed to create server asset partial sig: %w", err)
	}

	userPartialSig, _, err := user.partialSignAssetTransfer(fundedPkt,
		&arkSpendingDetails.arkAssetScript.cooperativeSpend, arkSpendingDetails.userScriptKey.RawKey, arkSpendingDetails.arkAssetScript.userNonce, arkSpendingDetails.serverScriptKey.RawKey.PubKey, arkSpendingDetails.arkAssetScript.serverNonce.PubNonce)
	if err != nil {
		return fmt.Errorf("failed to create user asset partial sig: %w", err)
	}

	transferAssetWitness, err := server.combineSigs(serverSessionId, userPartialSig, arkSpendingDetails.arkAssetScript.cooperativeSpend, arkSpendingDetails.arkAssetScript.tree, arkSpendingDetails.arkAssetScript.controlBlock)
	if err != nil {
		return fmt.Errorf("failed to combine sigs: %v", err)
	}

	for idx := range fundedPkt.Outputs {
		asset := fundedPkt.Outputs[idx].Asset
		firstPrevWitness := &asset.PrevWitnesses[0]
		if asset.HasSplitCommitmentWitness() {
			rootAsset := firstPrevWitness.SplitCommitment.RootAsset
			firstPrevWitness = &rootAsset.PrevWitnesses[0]
		}
		firstPrevWitness.TxWitness = transferAssetWitness
	}

	changeOutput := fundedPkt.Outputs[CHANGE_OUTPUT_INDEX]
	changeOutput.AnchorOutputInternalKey = asset.NUMSPubKey

	return nil
}

// CreateBtcWitness creates BTC witness for multiple inputs.
func CreateBtcWitness(arkSpendingDetails []ArkSpendingDetails, btcPacket *psbt.Packet, inputLength int, user, server *TapClient) ([]wire.TxWitness, error) {
	serverbtcControlBytesList := make([][]byte, inputLength)
	serverkeys := make([]keychain.KeyDescriptor, inputLength)
	serverTapLeaves := make([]txscript.TapLeaf, inputLength)

	for i := 0; i < inputLength; i++ {
		controlBlockBytes, err := arkSpendingDetails[i].arkBtcScript.controlBlock.ToBytes()
		if err != nil {
			return nil, fmt.Errorf("cannot convert control block to bytes %v", err)
		}
		serverbtcControlBytesList[i] = controlBlockBytes
		serverkeys[i] = arkSpendingDetails[i].serverInternalKey
		serverTapLeaves[i] = arkSpendingDetails[i].arkBtcScript.cooperativeSpend

	}

	serverBtcPartialSigs, err := server.partialSignBtcTransfer(
		btcPacket, inputLength,
		serverkeys, serverbtcControlBytesList, serverTapLeaves,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create server btc partial sigs %v", err)
	}

	userbtcControlBytesList := make([][]byte, inputLength)
	userkeys := make([]keychain.KeyDescriptor, inputLength)
	userTapLeaves := make([]txscript.TapLeaf, inputLength)

	for i := 0; i < inputLength; i++ {
		controlBlockBytes, err := arkSpendingDetails[i].arkBtcScript.controlBlock.ToBytes()
		if err != nil {
			return nil, fmt.Errorf("failed to convert user control block to bytes: %v", err)
		}
		userbtcControlBytesList[i] = controlBlockBytes
		userkeys[i] = arkSpendingDetails[i].userInternalKey
		userTapLeaves[i] = arkSpendingDetails[i].arkBtcScript.cooperativeSpend

	}

	userBtcPartialSigs, err := user.partialSignBtcTransfer(
		btcPacket, inputLength,
		userkeys, userbtcControlBytesList, userTapLeaves,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create user btc partial sigs: %v", err)
	}

	// created btc partial sig for user
	txwitnessList := make([]wire.TxWitness, inputLength)
	for i := 0; i < inputLength; i++ {
		txwitnessList[i] = wire.TxWitness{
			serverBtcPartialSigs[i],
			userBtcPartialSigs[i],
			arkSpendingDetails[i].arkBtcScript.cooperativeSpend.Script,
			serverbtcControlBytesList[i],
		}
	}

	return txwitnessList, nil
}
