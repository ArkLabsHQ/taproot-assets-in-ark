package taponark

import (
	"log"

	"github.com/btcsuite/btcd/rpcclient"
)

func GetBitcoinClient() *rpcclient.Client {
	// Set up the connection configuration for your btcd RPC server.
	connCfg := &rpcclient.ConnConfig{
		Host:         "localhost:18443", // btcd's RPC host:port
		User:         "polaruser",       // RPC username
		Pass:         "polarpass",       // RPC password
		HTTPPostMode: true,              // btcd only supports HTTP POST mode
		DisableTLS:   true,              // Use TLS if configured
	}

	// Create a new RPC client instance.
	client, err := rpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatalf("Error creating new RPC client: %v", err)
	}
	return client

}
