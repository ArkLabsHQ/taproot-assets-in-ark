package taponark

import (
	"log"

	"github.com/btcsuite/btcd/txscript"
	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/lightninglabs/taproot-assets/asset"
)

// user tap client

func SpendToBoardingTransaction(assetId []byte, amnt uint64, lockHeight uint32, userTapClient, serverTapClient *TapClient) ArkBoardingTransfer {

	addr, boardingKeys := CreateAssetKeys(assetId, amnt, userTapClient, serverTapClient)

	// 2. Send From User To Boarding Address
	sendResp, err := userTapClient.SendAsset(addr)

	if err != nil {
		log.Fatalf("cannot send to address %v", err)
	}

	// spend from Boarding Address
	btcInternalKey := asset.NUMSPubKey
	btcControlBlock := &txscript.ControlBlock{
		LeafVersion: txscript.BaseLeafVersion,
		InternalKey: btcInternalKey,
	}
	// Transfer is always output 1
	transferOutput := sendResp.Transfer.Outputs[BOARDING_TRANSFER_OUTPUT_INDEX]
	multiSigOutAnchor := transferOutput.Anchor
	rightNodeHash := boardingKeys.arkScript.unilateralSpend.TapHash()
	inclusionproof := append(rightNodeHash[:], multiSigOutAnchor.TaprootAssetRoot[:]...)
	btcControlBlock.InclusionProof = inclusionproof
	rootHash := btcControlBlock.RootHash(boardingKeys.arkScript.cooperativeSpend.Script)
	tapKey := txscript.ComputeTaprootOutputKey(btcInternalKey, rootHash)
	if tapKey.SerializeCompressed()[0] ==
		secp256k1.PubKeyFormatCompressedOdd {

		btcControlBlock.OutputKeyYIsOdd = true
	}

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
	arkTransfer := ArkTransfer{btcControlBlock, boardingKeys}

	log.Println("Boarding Transaction Published Onchain")
	return ArkBoardingTransfer{arkTransfer, transferOutput, amnt, userTapClient}
}
