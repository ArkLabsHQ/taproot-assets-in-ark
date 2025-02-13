package taponark

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/btcec/v2/schnorr/musig2"
	"github.com/btcsuite/btcd/btcutil/psbt"
	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/lndclient"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/fn"
	"github.com/lightninglabs/taproot-assets/tappsbt"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightninglabs/taproot-assets/taprpc/assetwalletrpc"
	"github.com/lightninglabs/taproot-assets/tapsend"

	"github.com/lightningnetwork/lnd/keychain"
	"github.com/lightningnetwork/lnd/lncfg"
	"github.com/lightningnetwork/lnd/lnrpc/signrpc"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
	"github.com/lightningnetwork/lnd/lntest/wait"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

func deserializeVPacket(packetBytes []byte) *tappsbt.VPacket {
	p, err := tappsbt.NewFromRawBytes(bytes.NewReader(packetBytes), false)

	if err != nil {

	}
	return p
}

type TapClient struct {
	client          taprpc.TaprootAssetsClient
	wallet          assetwalletrpc.AssetWalletClient
	lndClient       LndClient
	serverLndClient LndClient
	closeClient     func()
}

func InitTapClient(hostPort, tapdport, tlsData, macaroonData string, lndClient LndClient) TapClient {
	clientConn, err := NewBasicConn(hostPort, tapdport, tlsData, macaroonData)

	if err != nil {
		log.Fatalf("cannot initiate client")
	}

	cleanUp := func() {
		clientConn.Close()
	}

	return TapClient{client: taprpc.NewTaprootAssetsClient(clientConn), wallet: assetwalletrpc.NewAssetWalletClient(clientConn),
		closeClient: cleanUp,
		lndClient:   lndClient,
	}

}

func (cl *TapClient) GetBoardingAddress(scriptBranch txscript.TapBranch, assetScriptKey asset.ScriptKey, assetId []byte, amnt uint64) (*taprpc.Addr, error) {

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

	return addr, nil
}

func (cl *TapClient) GetNextKeys() (asset.ScriptKey,
	keychain.KeyDescriptor) {

	scriptKeyDesc, err := cl.wallet.NextScriptKey(
		context.TODO(), &assetwalletrpc.NextScriptKeyRequest{
			KeyFamily: uint32(asset.TaprootAssetsKeyFamily),
		},
	)

	if err != nil {
		log.Fatal(err)
	}

	scriptKey, err := taprpc.UnmarshalScriptKey(scriptKeyDesc.ScriptKey)
	if err != nil {
		log.Fatal(err)
	}

	internalKeyDesc, err := cl.wallet.NextInternalKey(
		context.TODO(), &assetwalletrpc.NextInternalKeyRequest{
			KeyFamily: uint32(asset.TaprootAssetsKeyFamily),
		},
	)
	if err != nil {
		log.Fatal(err)
	}

	internalKey, err := taprpc.UnmarshalKeyDescriptor(
		internalKeyDesc.InternalKey,
	)
	if err != nil {
		log.Fatal(err)
	}

	return *scriptKey, internalKey
}

func (cl *TapClient) IncomingTransferEvent(addr *taprpc.Addr) {
	_ = wait.NoError(func() error {
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

		log.Println("Got address event ")

		return nil
	}, time.Minute)

	// if err != nil {
	// 	log.Fatal("cannot fetch address event")
	// }
}

func (cl *TapClient) createMuSig2Session(
	localKey keychain.KeyDescriptor, otherKey []byte,
	localNonces musig2.Nonces, otherNonces [][]byte) []byte {

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
		log.Fatal(err)
	}

	return sess.SessionId
}

func (cl *TapClient) combineSigs(sessID,
	otherPartialSig []byte, leafToSign txscript.TapLeaf,
	tree *txscript.IndexedTapScriptTree,
	controlBlock *txscript.ControlBlock) wire.TxWitness {

	resp, err := cl.lndClient.client.MuSig2CombineSig(
		context.TODO(), &signrpc.MuSig2CombineSigRequest{
			SessionId:              sessID,
			OtherPartialSignatures: [][]byte{otherPartialSig},
		},
	)

	if err != nil {
		log.Fatal(err)
	}

	for _, leaf := range tree.LeafMerkleProofs {
		if leaf.TapHash() == leafToSign.TapHash() {
			controlBlock.InclusionProof = leaf.InclusionProof
		}
	}

	controlBlockBytes, err := controlBlock.ToBytes()
	if err != nil {
		log.Fatal(err)
	}

	commitmentWitness := make(wire.TxWitness, 3)
	commitmentWitness[0] = resp.FinalSignature
	commitmentWitness[1] = leafToSign.Script
	commitmentWitness[2] = controlBlockBytes

	return commitmentWitness
}

func (cl *TapClient) CommitVirtualPsbts(
	packet *psbt.Packet, activePackets []*tappsbt.VPacket,
	passivePackets []*tappsbt.VPacket,
	changeOutputIndex int32) (*psbt.Packet, []*tappsbt.VPacket,
	[]*tappsbt.VPacket, *assetwalletrpc.CommitVirtualPsbtsResponse) {

	var buf bytes.Buffer
	err := packet.Serialize(&buf)
	if err != nil {
		log.Fatal(err)
	}

	request := &assetwalletrpc.CommitVirtualPsbtsRequest{
		AnchorPsbt: buf.Bytes(),
		Fees: &assetwalletrpc.CommitVirtualPsbtsRequest_SatPerVbyte{
			// TODO: Verify this
			SatPerVbyte: uint64(2000 / 1000),
		},
	}

	type existingIndex = assetwalletrpc.CommitVirtualPsbtsRequest_ExistingOutputIndex
	if changeOutputIndex < 0 {
		request.AnchorChangeOutput = &assetwalletrpc.CommitVirtualPsbtsRequest_Add{
			Add: true,
		}
	} else {
		request.AnchorChangeOutput = &existingIndex{
			ExistingOutputIndex: changeOutputIndex,
		}
	}

	request.VirtualPsbts = make([][]byte, len(activePackets))
	for idx := range activePackets {
		request.VirtualPsbts[idx], err = tappsbt.Encode(
			activePackets[idx],
		)
		if err != nil {
			log.Fatal(err)
		}

	}
	request.PassiveAssetPsbts = make([][]byte, len(passivePackets))
	for idx := range passivePackets {
		request.PassiveAssetPsbts[idx], err = tappsbt.Encode(
			passivePackets[idx],
		)
		if err != nil {
			log.Fatal(err)
		}

	}

	// Now we can map the virtual packets to the PSBT.
	commitResponse, err := cl.wallet.CommitVirtualPsbts(context.TODO(), request)
	if err != nil {
		log.Fatal(err)
	}

	fundedPacket, err := psbt.NewFromRawBytes(
		bytes.NewReader(commitResponse.AnchorPsbt), false,
	)
	if err != nil {
		log.Fatal(err)
	}

	activePackets = make(
		[]*tappsbt.VPacket, len(commitResponse.VirtualPsbts),
	)
	for idx := range commitResponse.VirtualPsbts {
		activePackets[idx], err = tappsbt.Decode(
			commitResponse.VirtualPsbts[idx],
		)
		if err != nil {
			log.Fatal(err)
		}

	}

	passivePackets = make(
		[]*tappsbt.VPacket, len(commitResponse.PassiveAssetPsbts),
	)
	for idx := range commitResponse.PassiveAssetPsbts {
		passivePackets[idx], err = tappsbt.Decode(
			commitResponse.PassiveAssetPsbts[idx],
		)
		if err != nil {
			log.Fatal(err)
		}

	}

	return fundedPacket, activePackets, passivePackets, commitResponse
}

func (cl *TapClient) FinalizePacket(
	pkt *psbt.Packet) *psbt.Packet {

	var buf bytes.Buffer
	err := pkt.Serialize(&buf)
	if err != nil {
		log.Fatal(err)
	}

	finalizeResp, err := cl.lndClient.wallet.FinalizePsbt(context.TODO(), &walletrpc.FinalizePsbtRequest{
		FundedPsbt: buf.Bytes(),
	})

	if err != nil {
		log.Fatal(err)
	}

	signedPacket, err := psbt.NewFromRawBytes(
		bytes.NewReader(finalizeResp.SignedPsbt), false,
	)
	if err != nil {
		log.Fatal(err)
	}

	return signedPacket
}

func (cl *TapClient) LogAndPublish(
	btcPkt *psbt.Packet, activeAssets []*tappsbt.VPacket,
	passiveAssets []*tappsbt.VPacket,
	commitResp *assetwalletrpc.CommitVirtualPsbtsResponse) *taprpc.SendAssetResponse {

	var buf bytes.Buffer
	err := btcPkt.Serialize(&buf)
	if err != nil {
		log.Fatal(err)
	}

	request := &assetwalletrpc.PublishAndLogRequest{
		AnchorPsbt:        buf.Bytes(),
		VirtualPsbts:      make([][]byte, len(activeAssets)),
		PassiveAssetPsbts: make([][]byte, len(passiveAssets)),
		ChangeOutputIndex: commitResp.ChangeOutputIndex,
		LndLockedUtxos:    commitResp.LndLockedUtxos,
	}

	for idx := range activeAssets {
		request.VirtualPsbts[idx], err = tappsbt.Encode(
			activeAssets[idx],
		)
		if err != nil {
			log.Fatal(err)
		}

	}
	for idx := range passiveAssets {
		request.PassiveAssetPsbts[idx], err = tappsbt.Encode(
			passiveAssets[idx],
		)
		if err != nil {
			log.Fatal(err)
		}

	}

	resp, err := cl.wallet.PublishAndLogTransfer(context.TODO(), request)
	if err != nil {
		log.Fatal(err)
	}

	return resp
}

func (cl *TapClient) partialSignBtcTransfer(pkt *psbt.Packet, inputIndex uint32,
	key keychain.KeyDescriptor, controlBlockBytes []byte,
	tapLeaf txscript.TapLeaf) []byte {

	leafToSign := []*psbt.TaprootTapLeafScript{{
		ControlBlock: controlBlockBytes,
		Script:       tapLeaf.Script,
		LeafVersion:  tapLeaf.LeafVersion,
	}}

	// The lnd SignPsbt RPC doesn't really understand multi-sig yet, we
	// cannot specify multiple keys that need to sign. So what we do here
	// is just replace the derivation path info for the input we want to
	// sign to the key we want to sign with. If we do this for every signing
	// participant, we'll get the correct signatures for OP_CHECKSIGADD.
	signInput := &pkt.Inputs[inputIndex]
	derivation, trDerivation := tappsbt.Bip32DerivationFromKeyDesc(
		key, chaincfg.RegressionNetParams.HDCoinType,
	)
	trDerivation.LeafHashes = [][]byte{fn.ByteSlice(tapLeaf.TapHash())}
	signInput.Bip32Derivation = []*psbt.Bip32Derivation{derivation}
	signInput.TaprootBip32Derivation = []*psbt.TaprootBip32Derivation{
		trDerivation,
	}
	signInput.TaprootLeafScript = leafToSign
	signInput.SighashType = txscript.SigHashDefault

	var buf bytes.Buffer
	err := pkt.Serialize(&buf)
	if err != nil {
		log.Fatal(err)
	}

	resp, err := cl.lndClient.wallet.SignPsbt(
		context.TODO(), &walletrpc.SignPsbtRequest{
			FundedPsbt: buf.Bytes(),
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	result, err := psbt.NewFromRawBytes(
		bytes.NewReader(resp.SignedPsbt), false,
	)
	if err != nil {
		log.Fatal(err)
	}
	// Make sure the input we wanted to sign for was actually signed.
	// require.Contains(t, resp.SignedInputs, inputIndex)

	return result.Inputs[inputIndex].TaprootScriptSpendSig[0].Signature
}

func (cl *TapClient) partialSignAssetTransfer(assetTransferPacket *tappsbt.VPacket, assetLeaf *txscript.TapLeaf, localScriptKeyDescriptor keychain.KeyDescriptor,
	localNonces *musig2.Nonces, remoteScriptKey *secp256k1.PublicKey, remoteNonce [66]byte) ([]byte, []byte) {
	sessID := cl.createMuSig2Session(localScriptKeyDescriptor, remoteScriptKey.SerializeCompressed(), *localNonces,
		[][]byte{remoteNonce[:]},
	)

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
		}, chaincfg.RegressionNetParams.HDCoinType,
	)
	vIn.Bip32Derivation = []*psbt.Bip32Derivation{derivation}
	vIn.TaprootBip32Derivation = []*psbt.TaprootBip32Derivation{
		trDerivation,
	}

	err := tapsend.SignVirtualTransaction(
		assetTransferPacket, partialSigner, partialSigner,
	)
	if err != nil {
		log.Fatal(err)
	}

	isSplit, err := assetTransferPacket.HasSplitCommitment()
	if err != nil {
		log.Fatal(err)
	}

	// Identify new output asset. For splits, the new asset that received
	// the signature is the one with the split root set to true.
	newAsset := assetTransferPacket.Outputs[0].Asset
	if isSplit {
		splitOut, err := assetTransferPacket.SplitRootOutput()
		if err != nil {
			log.Fatal(err)
		}

		newAsset = splitOut.Asset
	}

	// The first part of the witness is just a fake R value, which we can
	// ignore.
	partialSig := newAsset.PrevWitnesses[0].TxWitness[0][32:]

	return partialSig, sessID
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

// parseTLSAndMacaroon
func parseTLSAndMacaroon(tlsData, macData string) (credentials.TransportCredentials,
	*macaroon.Macaroon, error) {

	tlsBytes, err := hex.DecodeString(tlsData)

	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(tlsBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, nil, errors.New("failed to decode PEM block " +
			"containing tls certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	// Load the specified TLS certificate and build transport
	// credentials.
	creds := credentials.NewClientTLSFromCert(pool, "")

	var macBytes []byte
	mac := &macaroon.Macaroon{}
	// Load the specified macaroon file.
	macBytes, err = hex.DecodeString(macData)
	if err != nil {
		return nil, nil, err
	}

	if err = mac.UnmarshalBinary(macBytes); err != nil {
		return nil, nil, fmt.Errorf("unable to decode macaroon: %v",
			err)
	}

	return creds, mac, nil
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
