package taponark

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/btcsuite/btcd/chaincfg"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/rpcclient"
	"github.com/btcsuite/btcd/wire"
	"github.com/lightningnetwork/lnd/lntest/wait"
)

type BitcoinSendTxResult struct {
	block       *wire.MsgBlock
	blockHeight int64
	txindex     int
}

type BitcoinClient struct {
	client      *rpcclient.Client
	chainParams chaincfg.Params
	timeout     time.Duration
}

func GetBitcoinClient(config BitcoinClientConfig, chainParams chaincfg.Params, timeout time.Duration) BitcoinClient {
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

	return BitcoinClient{client, chainParams, timeout}

}

func (b BitcoinClient) WaitForConfirmation(txhash chainhash.Hash) error {
	log.Println("awaiting block to be mined")
	err := wait.NoError(func() error {
		txInfo, err := b.client.GetRawTransactionVerbose(&txhash)
		if err == nil {
			if txInfo.Confirmations == 0 {
				return fmt.Errorf("transaction not confirmed with hash %s", txhash.String())
			}
		} else {
			if !strings.Contains(err.Error(), "-5: No such mempool transaction") {
				return fmt.Errorf("failed to get transaction: %w", err)
			}
		}
		return nil
	}, b.timeout)

	return err
}

func (b BitcoinClient) SendTransaction(transaction *wire.MsgTx) (BitcoinSendTxResult, error) {
	txhash, err := b.client.SendRawTransaction(transaction, true)
	if err != nil {
		return BitcoinSendTxResult{}, fmt.Errorf("cannot send raw transaction %v", err)
	}

	log.Println("awaiting confirmation")

	var bitcoinSendResult BitcoinSendTxResult

	err = wait.NoError(func() error {
		txInfo, err := b.client.GetRawTransactionVerbose(txhash)
		if err == nil {
			if txInfo.Confirmations == 0 {
				return fmt.Errorf("transaction not confirmed with hash %s", txhash.String())
			}
		} else {
			if !strings.Contains(err.Error(), "-5: No such mempool transaction") {
				return fmt.Errorf("failed to get transaction: %w", err)
			}
		}
		for {
			blockheight, err := b.client.GetBlockCount()
			if err != nil {
				return fmt.Errorf("cannot get blocokheight %v", err)
			}

			blockhash, err := b.client.GetBlockHash(blockheight)
			if err != nil {
				return fmt.Errorf("cannot get block  hash%v", err)
			}

			block, err := b.client.GetBlock(blockhash)
			if err != nil {
				return fmt.Errorf("cannot get block  %v", err)
			}

			for index, txn := range block.Transactions {
				if txn.TxHash().String() == txhash.String() {
					bitcoinSendResult = BitcoinSendTxResult{block, blockheight, index}
					return nil
				}
			}

		}

	}, b.timeout)

	return bitcoinSendResult, err
}
