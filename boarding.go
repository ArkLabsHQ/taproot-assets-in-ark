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

func SpendToBoardingTransaction(assetId []byte, asset_amnt uint64, btc_amnt uint64, boardingClient, serverTapClient *TapClient) ArkBoardingTransfer {

	/// 1. Send Asset From  Boarding User To Boarding Address
	assetSpendingDetails := CreateBoardingSpendingDetails(boardingClient, serverTapClient)
	arkBtcScript := assetSpendingDetails.arkBtcScript
	arkAssetScript := assetSpendingDetails.arkAssetScript
	addr_resp, err := serverTapClient.GetNewAddress(arkBtcScript.Branch, arkAssetScript.tapScriptKey, assetId, asset_amnt)
	if err != nil {
		log.Fatalf("cannot get address %v", err)
	}
	sendResp, err := boardingClient.SendAsset(addr_resp)
	if err != nil {
		log.Fatalf("cannot send to address %v", err)
	}

	assetTransferOutput := sendResp.Transfer.Outputs[BOARDING_TRANSFER_OUTPUT_INDEX]
	taprootAssetRoot := assetTransferOutput.Anchor.TaprootAssetRoot
	assetControlBlock := extractControlBlock(assetSpendingDetails.arkBtcScript, taprootAssetRoot)
	assetSpendingDetails.arkBtcScript.controlBlock = assetControlBlock
	assetTransferDetails := AssetTransferDetails{assetTransferOutput, assetSpendingDetails, asset_amnt}
	log.Println("Boarding Asset Transfered")

	/// 2. Send BTC From Boarding User To Boarding Address
	btcSpendingDetails := CreateBoardingSpendingDetails(boardingClient, serverTapClient)
	btcControlBlock := extractControlBlock(assetSpendingDetails.arkBtcScript, []byte{0})
	assetSpendingDetails.arkBtcScript.controlBlock = btcControlBlock
	rootHash := btcControlBlock.RootHash(arkBtcScript.cooperativeSpend.Script)
	outputKey := txscript.ComputeTaprootOutputKey(asset.NUMSPubKey, rootHash)
	pkScript, err := txscript.PayToTaprootScript(outputKey)
	if err != nil {
		log.Fatalf("cannot create Pay To Taproot Script")
	}

	btcSendResp, err := boardingClient.lndClient.wallet.SendOutputs(context.TODO(), &walletrpc.SendOutputsRequest{
		SatPerKw: 2000,
		Outputs: []*signrpc.TxOut{
			{
				Value:    int64(btc_amnt),
				PkScript: pkScript,
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

	var txout *wire.TxOut = nil
	var transferOutpoint *wire.OutPoint = nil
	for index, output := range msgTx.TxOut {
		if bytes.Equal(output.PkScript, pkScript) {
			txout = output
			transferOutpoint = &wire.OutPoint{
				Hash:  msgTx.TxHash(),
				Index: uint32(index),
			}
		}
	}

	if txout == nil || transferOutpoint == nil {
		log.Fatalf("Transfer Cannot be made")
	}

	btcTransferDetails := BtcTransferDetails{
		txout, transferOutpoint, btc_amnt, btcSpendingDetails,
	}

	log.Println("Boarding Bitcoin Transfered")

	//TODO (Joshua) Ensure To Improve
	bcoinClient := GetBitcoinClient()
	bcoinClient.MineBlock()

	serverTapClient.IncomingTransferEvent(addr_resp)

	log.Println("Boarding Transaction Published Onchain")
	return ArkBoardingTransfer{assetTransferDetails, btcTransferDetails, boardingClient}
}
