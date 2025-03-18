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
		txInfo, err := b.client.GetRawTransactionVerbose(&txhash)
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

func (b BitcoinClient) SendTransaction(transaction *wire.MsgTx, timeout time.Duration) BitcoinSendTxResult {
	txhash, err := b.client.SendRawTransaction(transaction, true)
	if err != nil {
		log.Fatalf("cannot send raw transaction %v", err)
	}

	log.Println("awaiting confirmation")

	var bitcoinSendResult BitcoinSendTxResult

	err = wait.NoError(func() error {
		txInfo, err := b.client.GetRawTransactionVerbose(txhash)
		if err != nil {
			return fmt.Errorf("failed to get transaction: %w", err)
		}
		if txInfo.Confirmations < 1 {
			return fmt.Errorf("transaction not confirmed with hash %s", txhash.String())
		}

		var blockhash chainhash.Hash
		err = chainhash.Decode(&blockhash, txInfo.BlockHash)
		if err != nil {
			return fmt.Errorf("cannot decode chainhash %v", err)
		}

		block, err := b.client.GetBlock(&blockhash)
		if err != nil {
			return fmt.Errorf("cannot get block %v", err)
		}

		blockheight, err := b.client.GetBlockCount()
		if err != nil {
			return fmt.Errorf("cannot get blocokheight %v", err)
		}

		bitcoinSendResult = BitcoinSendTxResult{block, blockheight}
		return nil
	}, timeout)

	if err != nil {
		log.Fatalf("Error In Sending Transaction %v", err)
	}

	return bitcoinSendResult
}
