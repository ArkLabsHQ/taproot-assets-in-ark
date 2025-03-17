package taponark

import (
	"fmt"
	"log"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"
	"github.com/lightningnetwork/lnd/lntest/wait"
)

type BitcoinSendTxResult struct {
	block       *wire.MsgBlock
	blockHeight int64
}

type BitcoinClient struct {
	client *rpcclient.Client
}

func GetBitcoinClient(config BitcoinClientConfig) BitcoinClient {
	hostPort := config.Host + ":" + config.Port

	connCfg := &rpcclient.ConnConfig{
		Host:         hostPort,        // btcd's RPC host:port
		User:         config.User,     // RPC username
		Pass:         config.Password, // RPC password
		HTTPPostMode: true,            // btcd only supports HTTP POST mode
		DisableTLS:   true,            // Use TLS if configured
	}

	// Create a new RPC client instance.
	client, err := rpcclient.New(connCfg, nil)
	if err != nil {
		log.Fatalf("Error creating new RPC client: %v", err)
	}
	return BitcoinClient{client}

}

func (b BitcoinClient) WaitForConfirmation(txhash chainhash.Hash, timeout time.Duration) error {
	log.Println("awaiting block to be mined")
	err := wait.NoError(func() error {
		txInfo, err := b.client.GetTransaction(&txhash)
		if err != nil {
			return fmt.Errorf("failed to get transaction: %w", err)
		}
		if txInfo.Confirmations == 0 {
			return fmt.Errorf("transaction not confirmed with hash %s", txhash.String())
		}
		return nil
	}, timeout)

	return err
}

func (b BitcoinClient) MineBlock() {
	address1, err := b.client.GetNewAddress("")
	if err != nil {
		log.Fatalf("cannot generate address %v", err)
	}
	maxretries := int64(5)
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
	maxretries := int64(5)
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
