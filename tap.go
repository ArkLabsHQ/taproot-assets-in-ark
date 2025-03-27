package taponark

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/lndclient"
	"github.com/lightninglabs/taproot-assets/address"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/fn"
	"github.com/lightninglabs/taproot-assets/proof"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/taprpc/assetwalletrpc"
	"github.com/lightninglabs/taproot-assets/taprpc/mintrpc"
	"github.com/lightninglabs/taproot-assets/taprpc/tapdevrpc"
	"github.com/lightninglabs/taproot-assets/taprpc/universerpc"
	"github.com/lightninglabs/taproot-assets/tapscript"
	"github.com/lightninglabs/taproot-assets/tapsend"

	"github.com/lightningnetwork/lnd/keychain"
	"github.com/lightningnetwork/lnd/lncfg"
	"github.com/lightningnetwork/lnd/lnrpc/signrpc"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
	"github.com/lightningnetwork/lnd/lntest/wait"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
)

type TapClient struct {
	universeHost   string
	client         taprpc.TaprootAssetsClient
	wallet         assetwalletrpc.AssetWalletClient
	devclient      tapdevrpc.TapDevClient
	mintclient     mintrpc.MintClient
	universeclient universerpc.UniverseClient
	lndClient      LndClient
	closeClient    func()
	chainParams    chaincfg.Params
	tapParams      address.ChainParams
	timeout        time.Duration
}

func InitTapClient(universeHost string, tapConfig TapClientConfig, lndClient LndClient, tlsCert, adminMacaroon string, chainParams chaincfg.Params, tapParams address.ChainParams, timeout time.Duration) TapClient {
	hostPort := tapConfig.Host + ":" + tapConfig.Port
	clientConn, err := NewBasicConn(hostPort, tapConfig.Port, tlsCert, adminMacaroon)

	if err != nil {
		log.Fatalf("cannot initiate client")
	}

	cleanUp := func() {
		clientConn.Close()
	}
	devclient := tapdevrpc.NewTapDevClient(clientConn)

	mintclient := mintrpc.NewMintClient(clientConn)
	universeclient := universerpc.NewUniverseClient(clientConn)

	return TapClient{
		universeHost:   universeHost,
		client:         taprpc.NewTaprootAssetsClient(clientConn),
		wallet:         assetwalletrpc.NewAssetWalletClient(clientConn),
		devclient:      devclient,
		mintclient:     mintclient,
		universeclient: universeclient,
		closeClient:    cleanUp,
		lndClient:      lndClient,
		chainParams:    chainParams,
		tapParams:      tapParams,
		timeout:        timeout,
	}

}

func (cl *TapClient) GetNewAddress(scriptBranch txscript.TapBranch, assetScriptKey asset.ScriptKey, assetId []byte, amnt uint64) (*taprpc.Addr, error) {

	btcInternalKey := asset.NUMSPubKey

	scriptBranchPreimage := commitment.NewPreimageFromBranch(scriptBranch)
	encodedBranchPreimage, _, err := commitment.MaybeEncodeTapscriptPreimage(&scriptBranchPreimage)

	if err != nil {
		return nil, err
	}

	fmt.Printf("%+v\n", encodedBranchPreimage)

	addr, err := cl.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId:   assetId,
		Amt:       amnt,
		ScriptKey: taprpc.MarshalScriptKey(assetScriptKey),
		InternalKey: &taprpc.KeyDescriptor{
			RawKeyBytes: btcInternalKey.SerializeCompressed(),
		},
		TapscriptSibling: encodedBranchPreimage,
	})

	if err != nil {
		return nil, fmt.Errorf("cannot Get new Asset Address %v", err)
	}

	return addr, nil
}

func (cl *TapClient) GetBtcAddress() (string, error) {
	addr, err := cl.lndClient.wallet.NextAddr(context.TODO(), &walletrpc.AddrRequest{
		Type:   walletrpc.AddressType_TAPROOT_PUBKEY,
		Change: false,
	})

	if err != nil {
		return "", fmt.Errorf("cannot Get new Btc Address %v", err)
	}

	return addr.Addr, nil
}

func (cl *TapClient) SendAsset(addr *taprpc.Addr) (*taprpc.SendAssetResponse, error) {
	return cl.client.SendAsset(
		context.TODO(), &taprpc.SendAssetRequest{
			TapAddrs: []string{addr.Encoded},
			FeeRate:  2000,
		},
	)
}

func (cl *TapClient) ExportProof(assetId []byte, scriptKey []byte) (*taprpc.ProofFile, error) {
	fullProof, err := cl.client.ExportProof(context.TODO(), &taprpc.ExportProofRequest{
		AssetId:   assetId,
		ScriptKey: scriptKey,
	})

	if err != nil {
		return nil, fmt.Errorf("cannot export proof %v", err)
	}

	return fullProof, nil
}

func (cl *TapClient) CreateAsset() ([]byte, error) {
	// Mint an asset into a downgraded anchor commitment.
	manualAssetName, err := RandomHexString(5)
	if err != nil {
		return nil, fmt.Errorf("cannot generate random string %v", err)
	}
	mintAsset := mintrpc.MintAsset{
		AssetVersion: 0,
		AssetType:    taprpc.AssetType_NORMAL,
		Name:         manualAssetName,
		AssetMeta: &taprpc.AssetMeta{
			Data: []byte("not metadata"),
		},
		Amount: 100_000,
	}
	req := mintrpc.MintAssetRequest{
		Asset:         &mintAsset,
		ShortResponse: true,
	}

	_, err = cl.mintclient.MintAsset(context.TODO(), &req)
	if err != nil {
		return nil, fmt.Errorf("cannot mint asset %v", err)
	}

	_, err = cl.mintclient.FinalizeBatch(context.TODO(), &mintrpc.FinalizeBatchRequest{
		ShortResponse: true,
	})
	if err != nil {
		return nil, fmt.Errorf("cannot finalise batch %v", err)
	}

	assetId, err := cl.IncomingMintEvent(manualAssetName)

	if err != nil {
		return nil, fmt.Errorf("cannot get asset %v", err)
	}

	return assetId, nil
}

func (cl *TapClient) GetBalance(assetId []byte) (uint64, int64, error) {
	assetbalanceResponse, err := cl.client.ListBalances(context.TODO(), &taprpc.ListBalancesRequest{
		GroupBy: &taprpc.ListBalancesRequest_AssetId{
			AssetId: true,
		},
		AssetFilter: assetId,
	})
	if err != nil {
		return 0, 0, fmt.Errorf("cannot get asset balance %v", err)
	}

	assetBalance := uint64(0)

	for _, balance := range assetbalanceResponse.AssetBalances {
		if assetId == nil {
			break
		}
		if bytes.Equal(balance.AssetGenesis.AssetId, assetId) {
			assetBalance = balance.Balance
			break
		}

	}

	btcBalanceResponse, err := cl.lndClient.wallet.ListAddresses(context.TODO(), &walletrpc.ListAddressesRequest{})

	if err != nil {
		return 0, 0, fmt.Errorf("cannot get btc balance %v", err)
	}

	btcBalance := int64(0)
	for _, bc := range btcBalanceResponse.AccountWithAddresses {
		for _, ac := range bc.Addresses {
			btcBalance += ac.Balance
		}
	}

	return assetBalance, btcBalance, nil
}

func (cl *TapClient) GetNextKeys() (asset.ScriptKey,
	keychain.KeyDescriptor, error) {

	scriptKeyDesc, err := cl.wallet.NextScriptKey(
		context.TODO(), &assetwalletrpc.NextScriptKeyRequest{
			KeyFamily: uint32(asset.TaprootAssetsKeyFamily),
		},
	)

	if err != nil {
		return asset.ScriptKey{}, keychain.KeyDescriptor{}, fmt.Errorf("cannot get script key %v", err)
	}

	scriptKey, err := taprpc.UnmarshalScriptKey(scriptKeyDesc.ScriptKey)
	if err != nil {
		return asset.ScriptKey{}, keychain.KeyDescriptor{}, fmt.Errorf("cannot unmarshal script key %v", err)
	}

	internalKeyDesc, err := cl.wallet.NextInternalKey(
		context.TODO(), &assetwalletrpc.NextInternalKeyRequest{
			KeyFamily: uint32(asset.TaprootAssetsKeyFamily),
		},
	)
	if err != nil {
		return asset.ScriptKey{}, keychain.KeyDescriptor{}, fmt.Errorf("cannot get internal key %v", err)
	}

	internalKey, err := taprpc.UnmarshalKeyDescriptor(
		internalKeyDesc.InternalKey,
	)
	if err != nil {
		return asset.ScriptKey{}, keychain.KeyDescriptor{}, fmt.Errorf("cannot unmarshal internal key %v", err)
	}

	return *scriptKey, internalKey, nil
}

func (cl *TapClient) Sync() error {
	_, err := cl.universeclient.SyncUniverse(context.TODO(), &universerpc.SyncRequest{
		UniverseHost: cl.universeHost,
		SyncMode:     universerpc.UniverseSyncMode_SYNC_FULL,
	})

	if err != nil {
		return fmt.Errorf("cannot sync %v", err)
	}

	return nil
}

func (cl *TapClient) IncomingMintEvent(assetName string) ([]byte, error) {
	var assetId []byte
	err := wait.NoError(func() error {
		resp, err := cl.client.ListAssets(
			context.TODO(), &taprpc.ListAssetRequest{
				IncludeSpent:            false,
				IncludeLeased:           false,
				IncludeUnconfirmedMints: false,
			},
		)
		if err != nil {
			return err
		}

		if len(resp.Assets) == 0 {
			return fmt.Errorf("no assets found")
		}

		for _, asset := range resp.Assets {
			if asset.AssetGenesis.Name == assetName {
				assetId = asset.AssetGenesis.AssetId
				return nil
			}
		}
		return fmt.Errorf("new assets not found")
	}, cl.timeout)

	return assetId, err
}

func (cl *TapClient) IncomingTransferEvent(addr *taprpc.Addr) error {
	err := wait.NoError(func() error {
		resp, err := cl.client.AddrReceives(
			context.TODO(), &taprpc.AddrReceivesRequest{
				FilterAddr: addr.Encoded,
			},
		)
		if err != nil {
			return err
		}

		if len(resp.Events) != 1 {
			return fmt.Errorf("got %d events, wanted %d",
				len(resp.Events), 1)
		}

		if resp.Events[0].Status != taprpc.AddrEventStatus_ADDR_EVENT_STATUS_COMPLETED {
			return fmt.Errorf("got status %v, wanted %v",
				resp.Events[0].Status, taprpc.AddrEventStatus_ADDR_EVENT_STATUS_COMPLETED)
		}
		return nil
	}, cl.timeout)

	return err
}

func (cl *TapClient) createMuSig2Session(
	localKey keychain.KeyDescriptor, otherKey []byte,
	localNonces musig2.Nonces, otherNonces [][]byte) ([]byte, error) {

	version := signrpc.MuSig2Version_MUSIG2_VERSION_V100RC2
	sess, err := cl.lndClient.client.MuSig2CreateSession(
		context.TODO(), &signrpc.MuSig2SessionRequest{
			KeyLoc: &signrpc.KeyLocator{
				KeyFamily: int32(localKey.Family),
				KeyIndex:  int32(localKey.Index),
			},
			AllSignerPubkeys: [][]byte{
				localKey.PubKey.SerializeCompressed(),
				otherKey,
			},
			OtherSignerPublicNonces: otherNonces,
			TaprootTweak: &signrpc.TaprootTweakDesc{
				KeySpendOnly: true,
			},
			Version:                version,
			PregeneratedLocalNonce: localNonces.SecNonce[:],
		},
	)

	if err != nil {
		return nil, fmt.Errorf("cannot create MuSig2 Session %v", err)
	}

	return sess.SessionId, nil
}

func (cl *TapClient) combineSigs(sessID,
	otherPartialSig []byte, leafToSign txscript.TapLeaf,
	tree *txscript.IndexedTapScriptTree,
	controlBlock *txscript.ControlBlock) (wire.TxWitness, error) {

	resp, err := cl.lndClient.client.MuSig2CombineSig(
		context.TODO(), &signrpc.MuSig2CombineSigRequest{
			SessionId:              sessID,
			OtherPartialSignatures: [][]byte{otherPartialSig},
		},
	)

	if err != nil {
		return wire.TxWitness{}, fmt.Errorf("cannot combine signature %v", err)
	}

	for _, leaf := range tree.LeafMerkleProofs {
		if leaf.TapHash() == leafToSign.TapHash() {
			controlBlock.InclusionProof = leaf.InclusionProof
		}
	}

	controlBlockBytes, err := controlBlock.ToBytes()
	if err != nil {
		return wire.TxWitness{}, fmt.Errorf("cannot Get control byte %v", err)
	}

	commitmentWitness := make(wire.TxWitness, 3)
	commitmentWitness[0] = resp.FinalSignature
	commitmentWitness[1] = leafToSign.Script
	commitmentWitness[2] = controlBlockBytes

	return commitmentWitness, nil
}

// Note: Commits Outputs pkscripts
func (cl *TapClient) CommitVirtualPsbts(
	fundedPacket *psbt.Packet, activePackets []*tappsbt.VPacket) error {

	outputCommitments := make(tappsbt.OutputCommitments)

	// And now we commit each packet to the respective anchor output
	// commitments.
	for _, vPkt := range activePackets {
		err := cl.commitPacket(vPkt, outputCommitments)
		if err != nil {
			return fmt.Errorf("error committing packet: %v", err)
		}
	}

	// Create and Update Taproot Output Keys
	for _, vPkt := range activePackets {
		err := tapsend.UpdateTaprootOutputKeys(
			fundedPacket, vPkt, outputCommitments,
		)
		if err != nil {
			return fmt.Errorf("error updating taproot output "+
				"keys: %v", err)
		}
	}

	// We're done creating the output commitments, we can now create the
	// transition proof suffixes.
	for idx := range activePackets {
		vPkt := activePackets[idx]

		for vOutIdx := range vPkt.Outputs {
			proofSuffix, err := tapsend.CreateProofSuffix(
				fundedPacket.UnsignedTx, fundedPacket.Outputs,
				vPkt, outputCommitments, vOutIdx, activePackets,
			)
			if err != nil {
				return fmt.Errorf("error creating proof suffix: %v", err)
			}

			vPkt.Outputs[vOutIdx].ProofSuffix = proofSuffix
		}
	}
	return nil
}

// commitPacket creates the output commitments for a virtual packet and merges
// it with the existing commitments for the anchor outputs.
func (cl *TapClient) commitPacket(vPkt *tappsbt.VPacket,
	outputCommitments tappsbt.OutputCommitments) error {

	inputs := vPkt.Inputs
	outputs := vPkt.Outputs

	// One virtual packet is only allowed to contain inputs and outputs of
	// the same asset ID. Fungible assets must be sent in separate packets.
	firstInputID := inputs[0].Asset().ID()
	for idx := range inputs {
		if inputs[idx].Asset().ID() != firstInputID {
			return fmt.Errorf("inputs must have the same asset ID")
		}
	}

	// Set the output commitment version based on the vPkt version.
	outputCommitmentVersion, err := tappsbt.CommitmentVersion(vPkt.Version)
	if err != nil {
		return err
	}

	for idx := range outputs {
		vOut := outputs[idx]
		anchorOutputIdx := vOut.AnchorOutputIndex

		if vOut.Asset == nil {
			return fmt.Errorf("output %d is missing asset", idx)
		}

		sendTapCommitment, err := commitment.FromAssets(
			outputCommitmentVersion, vOut.Asset,
		)
		if err != nil {
			return fmt.Errorf("error committing assets: %w", err)
		}

		// Because the receiver of this output might be receiving
		// through an address (non-interactive), we need to blank out
		// the split commitment proof, as the receiver doesn't know of
		// this information yet. The final commitment will be to a leaf
		// without the split commitment proof, that proof will be
		// delivered in the proof file as part of the non-interactive
		// send. We do the same even for interactive sends to not need
		// to distinguish between the two cases in the proof file
		// itself.
		sendTapCommitment, err = commitment.TrimSplitWitnesses(
			outputCommitmentVersion, sendTapCommitment,
		)
		if err != nil {
			return fmt.Errorf("error trimming split witnesses: %w",
				err)
		}

		// If the vOutput contains any AltLeaves, merge them into the
		// tap commitment.
		err = sendTapCommitment.MergeAltLeaves(vOut.AltLeaves)
		if err != nil {
			return fmt.Errorf("error merging alt leaves: %w", err)
		}

		// Merge the finished TAP level commitment with the existing
		// one (if any) for the anchor output.
		anchorOutputCommitment, ok := outputCommitments[anchorOutputIdx]
		if ok {
			err = sendTapCommitment.Merge(anchorOutputCommitment)
			if err != nil {
				return fmt.Errorf("unable to merge output "+
					"commitment: %w", err)
			}
		}

		outputCommitments[anchorOutputIdx] = sendTapCommitment

	}

	return nil
}

func (cl *TapClient) partialSignBtcTransfer(pkt *psbt.Packet, length int,
	keys []keychain.KeyDescriptor, controlBlockBytesList [][]byte,
	tapLeaves []txscript.TapLeaf) ([][]byte, error) {

	// The lnd SignPsbt RPC doesn't really understand multi-sig yet, we
	// cannot specify multiple keys that need to sign. So what we do here
	// is just replace the derivation path info for the input we want to
	// sign to the key we want to sign with. If we do this for every signing
	// participant, we'll get the correct signatures for OP_CHECKSIGADD.

	for i := 0; i < length; i++ {
		leafToSign := []*psbt.TaprootTapLeafScript{{
			ControlBlock: controlBlockBytesList[i],
			Script:       tapLeaves[i].Script,
			LeafVersion:  tapLeaves[i].LeafVersion,
		}}
		signInput := &pkt.Inputs[i]
		derivation, trDerivation := tappsbt.Bip32DerivationFromKeyDesc(
			keys[i], cl.chainParams.HDCoinType,
		)
		trDerivation.LeafHashes = [][]byte{fn.ByteSlice(tapLeaves[i].TapHash())}
		signInput.Bip32Derivation = []*psbt.Bip32Derivation{derivation}
		signInput.TaprootBip32Derivation = []*psbt.TaprootBip32Derivation{
			trDerivation,
		}
		signInput.TaprootLeafScript = leafToSign
		signInput.SighashType = txscript.SigHashDefault
	}

	err := pkt.SanityCheck()
	if err != nil {
		return nil, fmt.Errorf("error sanity checking packet: %v", err)
	}

	var buf bytes.Buffer
	err = pkt.Serialize(&buf)
	if err != nil {
		return nil, fmt.Errorf("error serializing packet: %v", err)
	}

	resp, err := cl.lndClient.wallet.SignPsbt(
		context.TODO(), &walletrpc.SignPsbtRequest{
			FundedPsbt: buf.Bytes(),
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error signing psbt: %v", err)
	}
	result, err := psbt.NewFromRawBytes(
		bytes.NewReader(resp.SignedPsbt), false,
	)
	if err != nil {
		return nil, fmt.Errorf("error parsing signed psbt: %v", err)
	}
	// Make sure the input we wanted to sign for was actually signed.
	// require.Contains(t, resp.SignedInputs, inputIndex)

	signatures := make([][]byte, length)
	for i := 0; i < length; i++ {
		signatures[i] = result.Inputs[i].TaprootScriptSpendSig[0].Signature
	}

	return signatures, nil
}

func (cl *TapClient) partialSignAssetTransfer(assetTransferPacket *tappsbt.VPacket, assetLeaf *txscript.TapLeaf, localScriptKeyDescriptor keychain.KeyDescriptor,
	localNonces *musig2.Nonces, remoteScriptKey *secp256k1.PublicKey, remoteNonce [66]byte) ([]byte, []byte, error) {
	sessID, err := cl.createMuSig2Session(localScriptKeyDescriptor, remoteScriptKey.SerializeCompressed(), *localNonces,
		[][]byte{remoteNonce[:]},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot create MuSig2 Session %v", err)
	}

	partialSigner := &muSig2PartialSigner{
		sessID:     sessID,
		lnd:        &cl.lndClient,
		leafToSign: *assetLeaf,
	}

	fmt.Printf("%+v\n", assetTransferPacket.Inputs)

	vIn := assetTransferPacket.Inputs[0]
	derivation, trDerivation := tappsbt.Bip32DerivationFromKeyDesc(
		keychain.KeyDescriptor{
			PubKey: localScriptKeyDescriptor.PubKey,
		}, cl.chainParams.HDCoinType,
	)
	vIn.Bip32Derivation = []*psbt.Bip32Derivation{derivation}
	vIn.TaprootBip32Derivation = []*psbt.TaprootBip32Derivation{
		trDerivation,
	}

	// Note: This also adds Split Commitment Root to Split Asset
	err = tapsend.SignVirtualTransaction(
		assetTransferPacket, partialSigner, partialSigner,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot Sign Virtual Transaction %v", err)
	}

	isSplit, err := assetTransferPacket.HasSplitCommitment()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot check for split commitment %v", err)
	}

	// Identify new output asset. For splits, the new asset that received
	// the signature is the one with the split root set to true.
	newAsset := assetTransferPacket.Outputs[0].Asset
	if isSplit {
		splitOut, err := assetTransferPacket.SplitRootOutput()
		if err != nil {
			return nil, nil, fmt.Errorf("cannot get split root output %v", err)
		}

		newAsset = splitOut.Asset
	}

	// The first part of the witness is just a fake R value, which we can
	// ignore.
	partialSig := newAsset.PrevWitnesses[0].TxWitness[0][32:]

	return partialSig, sessID, nil
}

func NewBasicConn(tapdHost string, tapdPort string, tlsPath, macPath string) (*grpc.ClientConn, error) {

	creds, mac, err := parseLndTLSAndMacaroon(
		tlsPath, macPath,
	)
	if err != nil {
		return nil, err
	}

	// Now we append the macaroon credentials to the dial options.
	cred, err := macaroons.NewMacaroonCredential(mac)
	if err != nil {
		return nil, fmt.Errorf("error creating macaroon credential: %v",
			err)
	}

	// Create a dial options array.
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(creds),
		grpc.WithPerRPCCredentials(cred),
		grpc.WithDefaultCallOptions(),
	}

	// We need to use a custom dialer so we can also connect to unix sockets
	// and not just TCP addresses.
	opts = append(
		opts, grpc.WithContextDialer(
			lncfg.ClientAddressDialer(tapdPort),
		),
	)
	conn, err := grpc.Dial(tapdHost, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to RPC server: %v",
			err)
	}

	return conn, nil
}

type muSig2PartialSigner struct {
	sessID     []byte
	lnd        *LndClient
	leafToSign txscript.TapLeaf
}

func (m *muSig2PartialSigner) ValidateWitnesses(*asset.Asset,
	[]*commitment.SplitAsset, commitment.InputSet) error {

	return nil
}

func (m *muSig2PartialSigner) SignVirtualTx(_ *lndclient.SignDescriptor,
	tx *wire.MsgTx, prevOut *wire.TxOut) (*schnorr.Signature, error) {

	prevOutputFetcher := txscript.NewCannedPrevOutputFetcher(
		prevOut.PkScript, prevOut.Value,
	)
	sighashes := txscript.NewTxSigHashes(tx, prevOutputFetcher)

	sigHash, err := txscript.CalcTapscriptSignaturehash(
		sighashes, txscript.SigHashDefault, tx, 0, prevOutputFetcher,
		m.leafToSign,
	)
	if err != nil {
		return nil, err
	}

	sign, err := m.lnd.client.MuSig2Sign(
		context.TODO(), &signrpc.MuSig2SignRequest{
			SessionId:     m.sessID,
			MessageDigest: sigHash,
			Cleanup:       false,
		},
	)
	if err != nil {
		return nil, err
	}

	// We only get the 32-byte partial signature (just the s value).
	// So we just use an all-zero value for R, since the parsing mechanism
	// doesn't validate R to be a valid point on the curve.
	var sig [schnorr.SignatureSize]byte
	copy(sig[32:], sign.LocalPartialSignature)

	return schnorr.ParseSignature(sig[:])
}

func (m *muSig2PartialSigner) Execute(*asset.Asset, []*commitment.SplitAsset,
	commitment.InputSet) error {

	return nil
}

// createAndSetAssetInput creates a virtual packet input for the given asset input
// and sets it on the given virtual packet.
func createAndSetAssetInput(vPkt *tappsbt.VPacket, idx int,
	roundDetails *taprpc.TransferOutput, assetId []byte) error {

	// At this point, we have a valid "coin" to spend in the commitment, so
	// we'll add the relevant information to the virtual TX's input.
	outpoint, err := wire.NewOutPointFromString(roundDetails.Anchor.Outpoint)
	if err != nil {
		return fmt.Errorf("cannot decode outpoint %w", err)
	}

	proof, err := proof.Decode(roundDetails.NewProofBlob)
	if err != nil {
		return fmt.Errorf("cannot decode proof %w", err)
	}

	internalKey, err := secp256k1.ParsePubKey(roundDetails.Anchor.InternalKey)
	if err != nil {
		return fmt.Errorf("cannot parse Pubkey %w", err)
	}

	tapKey := txscript.ComputeTaprootOutputKey(internalKey, roundDetails.Anchor.MerkleRoot)
	outputScript, err := tapscript.PayToTaprootScript(tapKey)
	if err != nil {
		return fmt.Errorf("cannot get TaprootScript %w", err)
	}

	prevID := asset.PrevID{
		OutPoint:  *outpoint,
		ID:        asset.ID(assetId),
		ScriptKey: asset.SerializedKey(roundDetails.ScriptKey),
	}

	vPkt.Inputs[idx] = &tappsbt.VInput{
		PrevID: prevID,
		Anchor: tappsbt.Anchor{
			Value:            btcutil.Amount(roundDetails.Anchor.Value),
			PkScript:         outputScript,
			InternalKey:      asset.NUMSPubKey,
			MerkleRoot:       roundDetails.Anchor.MerkleRoot,
			TapscriptSibling: roundDetails.Anchor.TapscriptSibling,
		},
		Proof: proof,
		PInput: psbt.PInput{
			SighashType: txscript.SigHashDefault,
		},
	}
	//Verify If This is the best way
	vPkt.SetInputAsset(idx, &proof.Asset)

	return nil
}

func createAndSetInputIntermediate(vPkt *tappsbt.VPacket,
	roundDetails ChainTransfer, assetId []byte) error {

	// At this point, we have a valid "coin" to spend in the commitment, so
	// we'll add the relevant information to the virtual TX's input.
	tapKey := txscript.ComputeTaprootOutputKey(roundDetails.internalKey, roundDetails.merkleRoot)
	outputScript, err := tapscript.PayToTaprootScript(tapKey)
	if err != nil {
		return fmt.Errorf("cannot get TaprootScript %v", err)
	}
	idx := 0
	prevID := asset.PrevID{
		OutPoint:  *roundDetails.outpoint,
		ID:        asset.ID(assetId),
		ScriptKey: asset.ToSerialized(roundDetails.scriptKey.PubKey),
	}

	vPkt.Inputs[idx] = &tappsbt.VInput{
		PrevID: prevID,
		Anchor: tappsbt.Anchor{
			Value:            btcutil.Amount(roundDetails.anchorValue),
			PkScript:         outputScript,
			InternalKey:      asset.NUMSPubKey,
			MerkleRoot:       roundDetails.merkleRoot,
			TapscriptSibling: roundDetails.taprootSibling,
		},
		Proof: roundDetails.transferProof,
		PInput: psbt.PInput{
			SighashType: txscript.SigHashDefault,
		},
	}
	//Verify If This is the best way
	vPkt.SetInputAsset(idx, &roundDetails.transferProof.Asset)

	return nil
}
