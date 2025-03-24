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

	"github.com/btcsuite/btcd/wire"
	"github.com/lightningnetwork/lnd/lncfg"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/signrpc"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
	"github.com/lightningnetwork/lnd/macaroons"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gopkg.in/macaroon.v2"
)

type LndClient struct {
	client      signrpc.SignerClient
	wallet      walletrpc.WalletKitClient
	closeClient func()
}

func InitLndClient(config LndClientConfig, tlsCert, adminMacaroon string) LndClient {
	hostPort := config.Host + ":" + config.Port
	clientConn, err := NewBasicLndConn(hostPort, config.Port, tlsCert, adminMacaroon)

	if err != nil {
		log.Fatalf("cannot initiate client")
	}

	cleanUp := func() {
		clientConn.Close()
	}

	return LndClient{client: signrpc.NewSignerClient(clientConn), wallet: walletrpc.NewWalletKitClient(clientConn), closeClient: cleanUp}

}

func (lc *LndClient) SendOutput(value int64, pkscript []byte) wire.MsgTx {
	response, err := lc.wallet.SendOutputs(context.TODO(), &walletrpc.SendOutputsRequest{
		SatPerKw: 2000,
		Outputs: []*signrpc.TxOut{
			{
				Value:    value,
				PkScript: pkscript,
			},
		},
		MinConfs:              1,
		SpendUnconfirmed:      false,
		CoinSelectionStrategy: lnrpc.CoinSelectionStrategy_STRATEGY_USE_GLOBAL_CONFIG,
	})
	if err != nil {
		log.Fatalf("cannot send btc to address %v", err)
	}
	// Deserialize the raw transaction bytes into the transaction.
	msgTx := wire.NewMsgTx(wire.TxVersion)
	if err := msgTx.Deserialize(bytes.NewReader(response.RawTx)); err != nil {
		log.Fatalf("failed to deserialize transaction: %v", err)
	}

	return *msgTx
}

func NewBasicLndConn(lndHost string, lndRpcPort, tlsPath, macPath string) (*grpc.ClientConn, error) {

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
			lncfg.ClientAddressDialer(lndRpcPort),
		),
	)
	conn, err := grpc.Dial(lndHost, opts...)
	if err != nil {
		return nil, fmt.Errorf("unable to connect to RPC server: %v",
			err)
	}

	return conn, nil
}

// parseLndTLSAndMacaroon
func parseLndTLSAndMacaroon(tlsData, macData string) (credentials.TransportCredentials,
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
