package taponark

import (
	"context"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/lightninglabs/taproot-assets/commitment"
	"github.com/lightninglabs/taproot-assets/taprpc"
	"github.com/lightningnetwork/lnd/lncfg"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

const (
	defaultDataDir         = "data"
	defaultTLSCertFilename = "2d2d2d2d2d424547494e2043455254494649434154452d2d2d2d2d0a4d4949434e544343416471674177494241674952414c63694458752b58413257774861775665354e454d6377436759494b6f5a497a6a3045417749774e4445670a4d4234474131554543684d58644746775a434268645852765a3256755a584a686447566b49474e6c636e51784544414f42674e5642414d5442324a76596931300a595841774868634e4d6a55774d6a41334d5459784e6a49355768634e4d6a59774e4441304d5459784e6a4935576a41304d534177486759445651514b457864300a5958426b494746316447396e5a57356c636d46305a57516759325679644445514d4134474131554541784d48596d39694c5852686344425a4d424d47427971470a534d34394167454743437147534d34394177454841304941424d4e6e577854306a576239567569592f47745951744c3170702b79327434734f563470547374410a6a53316c44394f515a454c565664482b675a42564d4f2b706154577149587a7a56304374693464334c454f77586a2b6a6763777767636b7744675944565230500a4151482f4241514441674b6b4d424d47413155644a51514d4d416f47434373474151554642774d424d41384741315564457745422f7751464d414d42416638770a485159445652304f424259454650707765574875666e304636742f762f4e576639693277677a4a4e4d48494741315564455152724d476d4342324a76596931300a5958434343577876593246736147397a64494948596d39694c585268634949526347397359584974626a45774c574a76596931305958434342485675615869430a436e56756158687759574e725a58534342324a315a6d4e76626d36484248384141414748454141414141414141414141414141414141414141414748424b77530a41414977436759494b6f5a497a6a3045417749445351417752674968414e5055314e47747858564f784e4c696358333542532f3344356559577a754d2b2f43760a32506a6d336f476d41694541346c6b5339306743576a3444553876414e687652635642555163437971677567584e354e73582b695361673d0a2d2d2d2d2d454e442043455254494649434154452d2d2d2d2d0a"
	defaultMacaroon        = "0201047461706402d001030a10cc26f487cc59326f82a7d7b42c991c661201301a180a09616464726573736573120472656164120577726974651a150a06617373657473120472656164120577726974651a170a086368616e6e656c73120472656164120577726974651a150a066461656d6f6e120472656164120577726974651a130a046d696e74120472656164120577726974651a150a0670726f6f6673120472656164120577726974651a120a03726671120472656164120577726974651a170a08756e69766572736512047265616412057772697465000006201b74fecdad1d43dbab27a1dbc46b2e21b535d03ecef5fdf9131729f867f9b72f"
	defaultRPCPort         = "12030"
	defaultRPCHostPort     = "localhost:" + defaultRPCPort

	groupKeyName         = "group_key"
	amtName              = "amt"
	assetVersionName     = "asset_version"
	addressVersionName   = "address_version"
	proofCourierAddrName = "proof_courier_addr"
)

type TapClient struct {
	client      taprpc.TaprootAssetsClient
	closeClient func()
}

func Init_client() TapClient {
	clientConn, err := NewBasicConn(defaultRPCHostPort, defaultTLSCertFilename, defaultMacaroon)

	if err != nil {
		log.Fatalf("cannot initiate client")
	}

	cleanUp := func() {
		clientConn.Close()
	}

	return TapClient{client: taprpc.NewTaprootAssetsClient(clientConn), closeClient: cleanUp}

}

func (cl *TapClient) GetBoardingAddress(user, server *btcec.PublicKey, locktime uint32, assetId []byte, amnt uint64) (*taprpc.Addr, error) {
	scriptBranch, err := CreateBoardingTapAddres(user, server, locktime)
	if err != nil {
		return nil, err
	}

	scriptBranchPreimage := commitment.NewPreimageFromBranch(*scriptBranch)
	encodedBranchPreimage, _, err := commitment.MaybeEncodeTapscriptPreimage(&scriptBranchPreimage)

	if err != nil {
		return nil, err
	}

	fmt.Printf("%+v\n", encodedBranchPreimage)

	addr, err := cl.client.NewAddr(context.TODO(), &taprpc.NewAddrRequest{
		AssetId:          assetId,
		Amt:              amnt,
		ScriptKey:        nil,
		InternalKey:      nil,
		TapscriptSibling: encodedBranchPreimage,
	})

	return addr, nil
}

// func get_tap_address(asset_id: []byte, tapbranch: []byte ){

// }

func NewBasicConn(tapdHost string, tlsPath, macPath string) (*grpc.ClientConn, error) {

	creds, mac, err := parseTLSAndMacaroon(
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
			lncfg.ClientAddressDialer(defaultRPCPort),
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
