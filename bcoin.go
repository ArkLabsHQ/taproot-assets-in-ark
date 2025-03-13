package taponark

import (
	"log"

	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"
)

type BitcoinSendTxResult struct {
	block       *wire.MsgBlock
	blockHeight int64
}

type BitcoinClient struct {
	client *rpcclient.Client
}

func GetBitcoinClient() BitcoinClient {
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
	return BitcoinClient{client}

}

func (b BitcoinClient) MineBlock() {
	address1, err := b.client.GetNewAddress("")
	if err != nil {
		log.Fatalf("cannot generate address %v", err)
	}
	maxretries := int64(3)
	_, err = b.client.GenerateToAddress(1, address1, &maxretries)
	if err != nil {
		log.Fatalf("cannot generate to address %v", err)
	}
}

func (b BitcoinClient) SendTransaction(transaction *wire.MsgTx) BitcoinSendTxResult {
	_, err := b.client.SendRawTransaction(transaction, true)
	if err != nil {
		log.Fatalf("cannot send raw transaction %v", err)
	}

	log.Println("transaction_sent")

	address1, err := b.client.GetNewAddress("")
	if err != nil {
		log.Fatalf("cannot generate address %v", err)
	}
	maxretries := int64(3)
	blockhash, err := b.client.GenerateToAddress(1, address1, &maxretries)
	if err != nil {
		log.Fatalf("cannot generate to address %v", err)
	}

	block, err := b.client.GetBlock(blockhash[0])
	if err != nil {
		log.Fatalf("cannot get block %v", err)
	}

	blockheight, err := b.client.GetBlockCount()
	if err != nil {
		log.Fatalf("cannot get block height %v", err)
	}

	return BitcoinSendTxResult{block, blockheight}
}
