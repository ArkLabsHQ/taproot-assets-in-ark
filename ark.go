package taponark

import (
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/address"
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

type ArkScript struct {
	cooperativeSpend txscript.TapLeaf
	unilateralSpend  txscript.TapLeaf
	Branch           txscript.TapBranch
}

type ArkAssetScript struct {
	userNonce    *musig2.Nonces
	serverNonce  *musig2.Nonces
	tapScriptKey asset.ScriptKey
	leaves       []txscript.TapLeaf
	tree         *txscript.IndexedTapScriptTree
	controlBlock *txscript.ControlBlock
}

type ArkTransfer struct {
	btcControlBlock *txscript.ControlBlock
	ArkAssetKeys    ArkAssetKeys
}

type ArkAssetKeys struct {
	userScriptKey     asset.ScriptKey
	userInternalKey   keychain.KeyDescriptor
	serverScriptKey   asset.ScriptKey
	serverInternalKey keychain.KeyDescriptor

	arkScript      ArkScript
	arkAssetScript ArkAssetScript
	address        *address.Tap
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
	arkTransferDetails ArkTransfer
	previousOutput     *taprpc.TransferOutput
	boardingAmount     uint64
	user               *TapClient
}

type ArkRoundChainTransfer struct {
	arkTransferDetails  ArkTransfer
	unpublishedTransfer ChainTransfer
}

func CreateAssetKeys(assetId []byte, amount uint64, user, server *TapClient) ArkAssetKeys {
	userScriptKey, userInternalKey := user.GetNextKeys()
	serverScriptKey, serverInternalKey := server.GetNextKeys()

	arkScript, err := CreateRoundArkScript(userInternalKey.PubKey, serverInternalKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}
	arkAssetScript := CreateRoundArkAssetScript(userScriptKey.RawKey.PubKey, serverScriptKey.RawKey.PubKey)
	if err != nil {
		log.Fatal(err)
	}

	addr_resp, err := server.GetBoardingAddress(arkScript.Branch, arkAssetScript.tapScriptKey, assetId, amount)
	if err != nil {
		log.Fatalf("cannot get address %v", err)
	}
	transferAddress, err := address.DecodeAddress(addr_resp.Encoded, &address.RegressionNetTap)
	if err != nil {
		log.Fatalf("cannot decode address %v", err)
	}

	return ArkAssetKeys{userScriptKey, userInternalKey, serverScriptKey, serverInternalKey, arkScript, arkAssetScript, transferAddress}

}

func CreateBoardingArkScript(user, server *btcec.PublicKey) (ArkScript, error) {
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
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return ArkScript{cooperativeSpend: leftLeaf, unilateralSpend: rightLeaf, Branch: branch}, nil
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

	leaves := make([]txscript.TapLeaf, 2)
	muSigTapscript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigUserServer.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatalf("Cannot create Musig Tapscript %v", err)
	}

	leaves[0] = txscript.TapLeaf{
		LeafVersion: txscript.BaseLeafVersion,
		Script:      muSigTapscript,
	}

	unilateralExit, err := txscript.NewScriptBuilder().
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(user)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatalf("Cannot create Unilateral Exit Script %v", err)
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

	return ArkAssetScript{userNonces, serverNonces, tapScriptKey, leaves, tree, controlBlock}
}

func CreateRoundArkScript(user, server *btcec.PublicKey) (ArkScript, error) {
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
		AddInt64(int64(LOCK_BLOCK_HEIGHT)).
		AddOp(txscript.OP_CHECKLOCKTIMEVERIFY).
		AddData(schnorr.SerializePubKey(server)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		return ArkScript{}, fmt.Errorf("failed to decode left script: %w", err)
	}

	rightLeaf := txscript.NewTapLeaf(txscript.BaseLeafVersion, rightLeafScript)

	branch := txscript.NewTapBranch(leftLeaf, rightLeaf)

	return ArkScript{cooperativeSpend: leftLeaf, unilateralSpend: rightLeaf, Branch: branch}, nil
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

	leaves := make([]txscript.TapLeaf, 2)
	muSigTapscript, err := txscript.NewScriptBuilder().
		AddData(schnorr.SerializePubKey(musigUserServer.FinalKey)).
		AddOp(txscript.OP_CHECKSIG).
		Script()

	if err != nil {
		log.Fatalf("Cannot create Musig Tapscript %v", err)
	}

	leaves[0] = txscript.TapLeaf{
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

	return ArkAssetScript{userNonces, serverNonces, tapScriptKey, leaves, tree, controlBlock}
}

func CreateAndInsertAssetWitness(arkTransferDetails ArkTransfer, fundedPkt *tappsbt.VPacket, user, server *TapClient) {
	_, serverSessionId := server.partialSignAssetTransfer(fundedPkt,
		&arkTransferDetails.arkAssetScript.leaves[0], arkTransferDetails.serverScriptKey.RawKey, arkTransferDetails.arkAssetScript.serverNonce, arkTransferDetails.userScriptKey.RawKey.PubKey, arkTransferDetails.arkAssetScript.userNonce.PubNonce)

	userPartialSig, _ := user.partialSignAssetTransfer(fundedPkt,
		&arkTransferDetails.arkAssetScript.leaves[0], arkTransferDetails.userScriptKey.RawKey, arkTransferDetails.arkAssetScript.userNonce, arkTransferDetails.serverScriptKey.RawKey.PubKey, arkTransferDetails.arkAssetScript.serverNonce.PubNonce)

	log.Println("created asset partial sig for user")

	log.Println("created asset partial for server")

	transferAssetWitness := server.combineSigs(serverSessionId, userPartialSig, arkTransferDetails.arkAssetScript.leaves[0], arkTransferDetails.arkAssetScript.tree, arkTransferDetails.arkAssetScript.controlBlock)

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

func CreateBtcWitness(arkTransferDetails ArkTransfer, btcPacket *psbt.Packet, user, server *TapClient) wire.TxWitness {
	btcControlBlockBytes, err := arkTransferDetails.btcControlBlock.ToBytes()
	if err != nil {
		log.Fatal(err)
	}
	assetInputIdx := uint32(TRANSFER_INPUT_INDEX)
	serverBtcPartialSig := server.partialSignBtcTransfer(
		btcPacket, assetInputIdx,
		arkTransferDetails.serverInternalKey, btcControlBlockBytes, arkTransferDetails.arkScript.cooperativeSpend,
	)
	userBtcPartialSig := user.partialSignBtcTransfer(
		btcPacket, assetInputIdx,
		arkTransferDetails.userInternalKey, btcControlBlockBytes, arkTransferDetails.arkScript.cooperativeSpend,
	)

	txWitness := wire.TxWitness{
		serverBtcPartialSig,
		userBtcPartialSig,
		arkTransferDetails.arkScript.cooperativeSpend.Script,
		btcControlBlockBytes,
	}

	return txWitness
}
