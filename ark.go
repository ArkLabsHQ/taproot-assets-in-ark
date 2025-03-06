package taponark

import (
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
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

type ProofTxMsg struct {
	Proof *proof.Proof
	TxMsg *wire.MsgTx
}

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

type ChainTransfer struct {
	finalTx          *wire.MsgTx
	outpoint         *wire.OutPoint
	transferProof    *proof.Proof
	merkleRoot       []byte
	taprootSibling   []byte
	internalKey      *btcec.PublicKey
	scriptKey        asset.ScriptKey
	anchorValue      int64
	taprootAssetRoot []byte
}

type ArkBoardingTransfer struct {
	assetTransferDetails AssetTransferDetails
	btcTransferDetails   BtcTransferDetails
	user                 *TapClient
}

type AssetTransferDetails struct {
	assetTransferOutput *taprpc.TransferOutput
	arkSpendingDetails  ArkSpendingDetails
	assetBoardingAmount uint64
}

type BtcTransferDetails struct {
	txout              *wire.TxOut
	outpoint           *wire.OutPoint
	btcBoardingAmount  uint64
	arkSpendingDetails ArkSpendingDetails
}

type ArkRoundChainTransfer struct {
	arkTransferDetails  ArkSpendingDetails
	unpublishedTransfer ChainTransfer
}

func CreateRoundSpendingDetails(assetId []byte, amount uint64, user, server *TapClient) ArkSpendingDetails {
	userScriptKey, userInternalKey := user.GetNextKeys()
	serverScriptKey, serverInternalKey := server.GetNextKeys()

	arkBtcScript, err := CreateRoundArkBtcScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateRoundArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: (JOSHUA, FIX)
	// addr_resp, err := server.GetNewAddress(arkBtcScript.Branch, arkAssetScript.tapScriptKey, assetId, amount)
	// if err != nil {
	// 	log.Fatalf("cannot get address %v", err)
	// }
	// transferAddress, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	// if err != nil {
	// 	log.Fatalf("cannot decode address %v", err)
	// }

	return ArkSpendingDetails{userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkBtcScript, arkAssetScript}

}

func CreateBoardingSpendingDetails(user, server *TapClient) ArkSpendingDetails {
	userScriptKey, userInternalKey := user.GetNextKeys()
	serverScriptKey, serverInternalKey := server.GetNextKeys()

	arkBtcScript, err := CreateBoardingArkBtcScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateBoardingArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}

	// TODO: (JOSHUA, FIX)
	// addr_resp, err := server.GetNewAddress(arkBtcScript.Branch, arkAssetScript.tapScriptKey, assetId, amount)
	// if err != nil {
	// 	log.Fatalf("cannot get address %v", err)
	// }
	// transferAddress, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	// if err != nil {
	// 	log.Fatalf("cannot decode address %v", err)
	// }

	return ArkSpendingDetails{userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkBtcScript, arkAssetScript}

}

func CreateBtcKeys(amount uint64, user, server *TapClient) ArkBtcKeys {
	_, userInternalKey := user.GetNextKeys()
	_, serverInternalKey := server.GetNextKeys()

	arkScript, err := CreateBoardingArkBtcScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}

	return ArkBtcKeys{arkScript, userInternalKey, serverInternalKey}

}

func CreateBoardingArkBtcScript(user, server *btcec.PublicKey) (ArkBtcScript, error) {
	leftLeafScript, err := txscript.NewScriptBuilder().
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

	leftLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, leftLeafScript)

	rightLeafScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkBtcScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return ArkBtcScript{cooperativeSpend: leftLeaf, unilateralSpend: rightLeaf, Branch: branch}, nil
}

func CreateBoardingArkAssetScript(
	user, server *btcec.PublicKey) ArkAssetScript {

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
		log.Fatal(err)
	}

	muSigTapscript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigUserServer.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatalf("Cannot create Musig Tapscript %v", err)
	}

	cooperativeSpend := txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      muSigTapscript,
	}

	sweep, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatalf("Cannot create Unilateral Exit Script %v", err)
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

	return ArkAssetScript{userNonces, serverNonces, tapScriptKey, cooperativeSpend, unilateralExit, tree, controlBlock}
}

func CreateRoundArkBtcScript(user, server *btcec.PublicKey) (ArkBtcScript, error) {
	leftLeafScript, err := txscript.NewScriptBuilder().
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

	leftLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, leftLeafScript)

	rightLeafScript, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkBtcScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return ArkBtcScript{cooperativeSpend: leftLeaf, unilateralSpend: rightLeaf, Branch: branch}, nil
}

func CreateRoundArkAssetScript(
	user, server *btcec.PublicKey) ArkAssetScript {

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
		log.Fatal(err)
	}

	muSigTapscript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigUserServer.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatalf("Cannot create Musig Tapscript %v", err)
	}

	cooperativeSpend := txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      muSigTapscript,
	}

	sweep, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatalf("Cannot create sweep Tapscript %v", err)
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

	return ArkAssetScript{userNonces, serverNonces, tapScriptKey, cooperativeSpend, unilateralExit, tree, controlBlock}
}

func CreateAndInsertAssetWitness(arkSpendingDetails ArkSpendingDetails, fundedPkt *tappsbt.VPacket, user, server *TapClient) {
	_, serverSessionId := server.partialSignAssetTransfer(fundedPkt,
		&arkSpendingDetails.arkAssetScript.cooperativeSpend, arkSpendingDetails.serverScriptKey.RawKey, arkSpendingDetails.arkAssetScript.serverNonce, arkSpendingDetails.userScriptKey.RawKey.PubKey, arkSpendingDetails.arkAssetScript.userNonce.PubNonce)

	log.Println("created asset partial for server")

	userPartialSig, _ := user.partialSignAssetTransfer(fundedPkt,
		&arkSpendingDetails.arkAssetScript.cooperativeSpend, arkSpendingDetails.userScriptKey.RawKey, arkSpendingDetails.arkAssetScript.userNonce, arkSpendingDetails.serverScriptKey.RawKey.PubKey, arkSpendingDetails.arkAssetScript.serverNonce.PubNonce)

	log.Println("created asset partial sig for user")

	transferAssetWitness := server.combineSigs(serverSessionId, userPartialSig, arkSpendingDetails.arkAssetScript.cooperativeSpend, arkSpendingDetails.arkAssetScript.tree, arkSpendingDetails.arkAssetScript.controlBlock)

	// update transferAsset Witnesss [Nothing Needs To Change]
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
}

// func CreateBtcWitness(arkTransferDetails ArkSpendingDetails, btcPacket *psbt.Packet, user, server *TapClient) wire.TxWitness {
// 	btcControlBlockBytes, err := arkTransferDetails.btcControlBlock.ToBytes()
// 	if err != nil {
// 		log.Fatal(err)
// 	}
// 	assetInputIdx := uint32(TRANSFER_INPUT_INDEX)
// 	serverBtcPartialSig := server.partialSignBtcTransfer(
// 		btcPacket, assetInputIdx,
// 		arkTransferDetails.ArkAssetKeys.serverInternalKey, btcControlBlockBytes, arkTransferDetails.ArkAssetKeys.arkScript.cooperativeSpend,
// 	)
// 	userBtcPartialSig := user.partialSignBtcTransfer(
// 		btcPacket, assetInputIdx,
// 		arkTransferDetails.ArkAssetKeys.userInternalKey, btcControlBlockBytes, arkTransferDetails.ArkAssetKeys.arkScript.cooperativeSpend,
// 	)

// 	txWitness := wire.TxWitness{
// 		serverBtcPartialSig,
// 		userBtcPartialSig,
// 		arkTransferDetails.ArkAssetKeys.arkScript.cooperativeSpend.Script,
// 		btcControlBlockBytes,
// 	}

// 	return txWitness
// }
