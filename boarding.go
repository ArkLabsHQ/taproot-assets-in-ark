package taponark

import (
	"bytes"
	"context"
	"log"

	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/lightninglabs/taproot-assets/asset"
	"github.com/lightningnetwork/lnd/lnrpc"
	"github.com/lightningnetwork/lnd/lnrpc/signrpc"
	"github.com/lightningnetwork/lnd/lnrpc/walletrpc"
)

// user tap client

func SpendToBoardingTransaction(assetId []byte, asset_amnt uint64, btc_amnt uint64, lockHeight uint32, boardingClient, serverTapClient *TapClient) ArkBoardingTransfer {

	addr, boardingKeys := CreateAssetKeys(assetId, asset_amnt, boardingClient, serverTapClient)

	// 2. Send Asset From  Boarding User To Boarding Address
	sendResp, err := boardingClient.SendAsset(addr)
	if err != nil {
		log.Fatalf("cannot send to address %v", err)
	}

	// 3. Send BTC From Boarding User To Boarding Address
	rootHash := boardingKeys.arkScript.Branch.TapHash()
	btcSendResp, err := boardingClient.lndClient.wallet.SendOutputs(context.TODO(), &walletrpc.SendOutputsRequest{
		SatPerKw: 2,
		Outputs: []*signrpc.TxOut{
			{
				Value:    int64(btc_amnt),
				PkScript: txscript.ComputeTaprootOutputKey(asset.NUMSPubKey, rootHash[:]).SerializeCompressed(),
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
	if err := msgTx.Deserialize(bytes.NewReader(btcSendResp.RawTx)); err != nil {
		log.Fatalf("failed to deserialize transaction: %v", err)
	}

	assetTransferOutput := sendResp.Transfer.Outputs[BOARDING_TRANSFER_OUTPUT_INDEX]
	taprootAssetRoot := assetTransferOutput.Anchor.TaprootAssetRoot
	controlBlock := extractControlBlock(boardingKeys.arkScript, taprootAssetRoot)

	//TODO (Joshua) Ensure To Improve
	log.Println("Asset Transfered")
	bcoinClient := GetBitcoinClient()
	address1, err := bcoinClient.client.GetNewAddress("")
	if err != nil {
		log.Fatalf("cannot generate address %v", err)
	}
	maxretries := int64(3)
	_, err = bcoinClient.client.GenerateToAddress(1, address1, &maxretries)
	if err != nil {
		log.Fatalf("cannot generate to address %v", err)
	}
	serverTapClient.IncomingTransferEvent(addr)
	arkTransfer := ArkTransfer{controlBlock, boardingKeys}

	log.Println("Boarding Transaction Published Onchain")
	return ArkBoardingTransfer{arkTransfer, assetTransferOutput, asset_amnt, btc_amnt, msgTx, boardingClient}
}
